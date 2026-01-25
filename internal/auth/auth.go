// Package auth provides authentication and authorization middleware for the Nexus API.
// It supports JWT tokens for API access and mTLS for device authentication.
package auth

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Common errors for authentication
var (
	ErrNoCredentials      = errors.New("no credentials provided")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenInvalid       = errors.New("token invalid")
	ErrCertificateInvalid = errors.New("certificate invalid")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("access forbidden")
)

// ContextKey is the type for context keys
type ContextKey string

const (
	// ContextKeyPrincipal is the key for the authenticated principal in request context
	ContextKeyPrincipal ContextKey = "auth.principal"
	// ContextKeyRequestID is the key for request ID in context
	ContextKeyRequestID ContextKey = "auth.request_id"
)

// PrincipalType identifies the type of authenticated entity
type PrincipalType string

const (
	PrincipalTypeDevice  PrincipalType = "device"  // Device authenticated via mTLS
	PrincipalTypeUser    PrincipalType = "user"    // User authenticated via JWT
	PrincipalTypeService PrincipalType = "service" // Service-to-service authentication
	PrincipalTypeAPIKey  PrincipalType = "apikey"  // API key authentication
)

// Principal represents an authenticated entity
type Principal struct {
	ID          string            // Unique identifier (device serial, user ID, etc.)
	Type        PrincipalType     // Type of principal
	Roles       []string          // Assigned roles for authorization
	Permissions []string          // Explicit permissions
	Metadata    map[string]string // Additional metadata
	ExpiresAt   time.Time         // When the authentication expires (for tokens)
	IssuedAt    time.Time         // When the credentials were issued
}

// HasRole checks if the principal has a specific role
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role || r == "admin" { // admin role has all roles
			return true
		}
	}
	return false
}

// HasPermission checks if the principal has a specific permission
func (p *Principal) HasPermission(permission string) bool {
	// Admin role has all permissions
	if p.HasRole("admin") {
		return true
	}
	for _, perm := range p.Permissions {
		if perm == permission || perm == "*" {
			return true
		}
	}
	return false
}

// Config holds authentication configuration
type Config struct {
	// JWT configuration
	JWTSecret          []byte        // Secret for HS256 signing (simple mode)
	JWTPublicKey       []byte        // Public key for RS256/ES256 verification
	JWTIssuer          string        // Expected issuer claim
	JWTAudience        []string      // Expected audience claim(s)
	JWTExpiration      time.Duration // Default token expiration (for token generation)
	JWTClockSkew       time.Duration // Allowed clock skew for expiration validation
	JWTRequiredClaims  []string      // Required claims in the token
	JWTRolesClaim      string        // Claim name for roles (default: "roles")
	JWTPermissionClaim string        // Claim name for permissions (default: "permissions")

	// mTLS configuration
	MTLSEnabled           bool                           // Enable mTLS authentication
	MTLSClientCAPool      *x509.CertPool                 // CA pool for client certificate validation
	MTLSRequireClientCert bool                           // Require client certificate (vs optional)
	MTLSDeviceIDExtractor func(*x509.Certificate) string // Extract device ID from cert

	// API Key configuration
	APIKeyEnabled    bool            // Enable API key authentication
	APIKeyHeader     string          // Header name for API key (default: X-API-Key)
	APIKeyQueryParam string          // Query param name for API key (optional)
	APIKeyValidator  APIKeyValidator // Custom API key validator

	// General configuration
	AllowAnonymous     bool     // Allow unauthenticated access to some endpoints
	AnonymousEndpoints []string // Endpoints that allow anonymous access
	RequireHTTPS       bool     // Require HTTPS for all requests

	// Rate limiting
	RateLimitEnabled     bool          // Enable rate limiting
	RateLimitRequests    int           // Maximum requests per window
	RateLimitWindow      time.Duration // Time window for rate limiting
	RateLimitByPrincipal bool          // Rate limit per principal (vs per IP)
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		JWTClockSkew:          5 * time.Minute,
		JWTExpiration:         24 * time.Hour,
		JWTRolesClaim:         "roles",
		JWTPermissionClaim:    "permissions",
		APIKeyHeader:          "X-API-Key",
		MTLSRequireClientCert: true,
		AnonymousEndpoints:    []string{"/health", "/ready"},
		RateLimitWindow:       time.Minute,
		RateLimitRequests:     100,
	}
}

// APIKeyValidator validates API keys and returns the associated principal
type APIKeyValidator interface {
	ValidateKey(ctx context.Context, key string) (*Principal, error)
}

// StaticAPIKeyValidator validates against a static list of API keys
type StaticAPIKeyValidator struct {
	mu   sync.RWMutex
	keys map[string]*Principal
}

// NewStaticAPIKeyValidator creates a new static API key validator
func NewStaticAPIKeyValidator() *StaticAPIKeyValidator {
	return &StaticAPIKeyValidator{
		keys: make(map[string]*Principal),
	}
}

// AddKey adds an API key with associated principal
func (v *StaticAPIKeyValidator) AddKey(key string, principal *Principal) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[key] = principal
}

// RemoveKey removes an API key
func (v *StaticAPIKeyValidator) RemoveKey(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.keys, key)
}

// ValidateKey validates an API key
func (v *StaticAPIKeyValidator) ValidateKey(ctx context.Context, key string) (*Principal, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	for storedKey, principal := range v.keys {
		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(key), []byte(storedKey)) == 1 {
			// Check if principal has expired
			if !principal.ExpiresAt.IsZero() && time.Now().After(principal.ExpiresAt) {
				return nil, ErrTokenExpired
			}
			return principal, nil
		}
	}
	return nil, ErrInvalidCredentials
}

// Authenticator handles authentication for HTTP requests
type Authenticator struct {
	config    *Config
	jwtParser *JWTParser
}

// NewAuthenticator creates a new authenticator with the given configuration
func NewAuthenticator(config *Config) (*Authenticator, error) {
	if config == nil {
		config = DefaultConfig()
	}

	auth := &Authenticator{
		config: config,
	}

	// Initialize JWT parser if JWT is configured
	if len(config.JWTSecret) > 0 || len(config.JWTPublicKey) > 0 {
		parser, err := NewJWTParser(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT parser: %w", err)
		}
		auth.jwtParser = parser
	}

	return auth, nil
}

// Authenticate extracts and validates credentials from the request
func (a *Authenticator) Authenticate(r *http.Request) (*Principal, error) {
	ctx := r.Context()

	// Check if this is an anonymous endpoint
	if a.isAnonymousEndpoint(r.URL.Path) {
		return nil, nil
	}

	// Try mTLS authentication first (if enabled and TLS connection present)
	if a.config.MTLSEnabled && r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		principal, err := a.authenticateMTLS(r.TLS.PeerCertificates[0])
		if err == nil {
			return principal, nil
		}
		// mTLS failed but might have other auth methods
		if a.config.MTLSRequireClientCert {
			return nil, err
		}
	}

	// Try JWT authentication from Authorization header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if a.jwtParser != nil {
			principal, err := a.jwtParser.ParseToken(ctx, token)
			if err == nil {
				return principal, nil
			}
			// Return error if Bearer token was provided but invalid
			return nil, err
		}
	}

	// Try API key authentication
	if a.config.APIKeyEnabled && a.config.APIKeyValidator != nil {
		// Check header
		apiKey := r.Header.Get(a.config.APIKeyHeader)
		if apiKey == "" && a.config.APIKeyQueryParam != "" {
			// Check query parameter
			apiKey = r.URL.Query().Get(a.config.APIKeyQueryParam)
		}
		if apiKey != "" {
			principal, err := a.config.APIKeyValidator.ValidateKey(ctx, apiKey)
			if err == nil {
				return principal, nil
			}
			return nil, err
		}
	}

	// No valid authentication found
	if a.config.AllowAnonymous {
		return nil, nil
	}
	return nil, ErrNoCredentials
}

// authenticateMTLS validates a client certificate and extracts the principal
func (a *Authenticator) authenticateMTLS(cert *x509.Certificate) (*Principal, error) {
	// Verify certificate against CA pool if configured
	if a.config.MTLSClientCAPool != nil {
		opts := x509.VerifyOptions{
			Roots: a.config.MTLSClientCAPool,
		}
		if _, err := cert.Verify(opts); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCertificateInvalid, err)
		}
	}

	// Extract device ID
	var deviceID string
	if a.config.MTLSDeviceIDExtractor != nil {
		deviceID = a.config.MTLSDeviceIDExtractor(cert)
	} else {
		// Default: use Common Name
		deviceID = cert.Subject.CommonName
	}

	if deviceID == "" {
		return nil, fmt.Errorf("%w: cannot extract device ID", ErrCertificateInvalid)
	}

	return &Principal{
		ID:        deviceID,
		Type:      PrincipalTypeDevice,
		Roles:     []string{"device"},
		IssuedAt:  cert.NotBefore,
		ExpiresAt: cert.NotAfter,
		Metadata: map[string]string{
			"serial": cert.SerialNumber.String(),
			"issuer": cert.Issuer.CommonName,
		},
	}, nil
}

// isAnonymousEndpoint checks if the endpoint allows anonymous access
func (a *Authenticator) isAnonymousEndpoint(path string) bool {
	for _, endpoint := range a.config.AnonymousEndpoints {
		if path == endpoint || strings.HasPrefix(path, endpoint+"/") {
			return true
		}
	}
	return false
}

// GetPrincipalFromContext retrieves the principal from request context
func GetPrincipalFromContext(ctx context.Context) *Principal {
	if principal, ok := ctx.Value(ContextKeyPrincipal).(*Principal); ok {
		return principal
	}
	return nil
}

// WithPrincipal adds a principal to the context
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, ContextKeyPrincipal, principal)
}
