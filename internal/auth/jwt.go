package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// JWTParser parses and validates JWT tokens
type JWTParser struct {
	config   *Config
	hmacKey  []byte
	rsaKey   *rsa.PublicKey
	ecdsaKey *ecdsa.PublicKey
}

// JWTClaims represents standard JWT claims plus custom claims
type JWTClaims struct {
	// Standard claims
	Subject   string   `json:"sub,omitempty"`
	Issuer    string   `json:"iss,omitempty"`
	Audience  []string `json:"aud,omitempty"`
	ExpiresAt int64    `json:"exp,omitempty"`
	IssuedAt  int64    `json:"iat,omitempty"`
	NotBefore int64    `json:"nbf,omitempty"`
	JTI       string   `json:"jti,omitempty"`

	// Custom claims for Nexus
	Roles       []string          `json:"roles,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Type        string            `json:"type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// JWTHeader represents the JWT header
type JWTHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid,omitempty"`
}

// NewJWTParser creates a new JWT parser
func NewJWTParser(config *Config) (*JWTParser, error) {
	parser := &JWTParser{
		config: config,
	}

	// Configure HMAC key if provided
	if len(config.JWTSecret) > 0 {
		parser.hmacKey = config.JWTSecret
	}

	// Parse public key if provided
	if len(config.JWTPublicKey) > 0 {
		block, _ := pem.Decode(config.JWTPublicKey)
		if block == nil {
			return nil, errors.New("failed to parse PEM block for public key")
		}

		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}

		switch key := pub.(type) {
		case *rsa.PublicKey:
			parser.rsaKey = key
		case *ecdsa.PublicKey:
			parser.ecdsaKey = key
		default:
			return nil, errors.New("unsupported public key type")
		}
	}

	if parser.hmacKey == nil && parser.rsaKey == nil && parser.ecdsaKey == nil {
		return nil, errors.New("no signing key configured")
	}

	return parser, nil
}

// ParseToken parses and validates a JWT token
func (p *JWTParser) ParseToken(ctx context.Context, tokenString string) (*Principal, error) {
	// Split token into parts
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: invalid token format", ErrTokenInvalid)
	}

	// Decode header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid header encoding", ErrTokenInvalid)
	}

	var header JWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header JSON", ErrTokenInvalid)
	}

	// Decode claims
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid claims encoding", ErrTokenInvalid)
	}

	var claims JWTClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: invalid claims JSON", ErrTokenInvalid)
	}

	// Verify signature
	signatureInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid signature encoding", ErrTokenInvalid)
	}

	if err := p.verifySignature(header.Algorithm, signatureInput, signature); err != nil {
		return nil, err
	}

	// Validate claims
	if err := p.validateClaims(&claims); err != nil {
		return nil, err
	}

	// Build principal from claims
	principal := &Principal{
		ID:          claims.Subject,
		Type:        PrincipalTypeUser,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
		Metadata:    claims.Metadata,
	}

	if claims.Type != "" {
		principal.Type = PrincipalType(claims.Type)
	}

	if claims.ExpiresAt > 0 {
		principal.ExpiresAt = time.Unix(claims.ExpiresAt, 0)
	}
	if claims.IssuedAt > 0 {
		principal.IssuedAt = time.Unix(claims.IssuedAt, 0)
	}

	return principal, nil
}

// verifySignature verifies the JWT signature
func (p *JWTParser) verifySignature(algorithm, input string, signature []byte) error {
	inputBytes := []byte(input)

	switch algorithm {
	case "HS256":
		if p.hmacKey == nil {
			return fmt.Errorf("%w: HMAC key not configured", ErrTokenInvalid)
		}
		mac := hmac.New(sha256.New, p.hmacKey)
		mac.Write(inputBytes)
		expected := mac.Sum(nil)
		if !hmac.Equal(signature, expected) {
			return fmt.Errorf("%w: signature verification failed", ErrTokenInvalid)
		}
		return nil

	case "RS256":
		if p.rsaKey == nil {
			return fmt.Errorf("%w: RSA key not configured", ErrTokenInvalid)
		}
		hash := sha256.Sum256(inputBytes)
		if err := rsa.VerifyPKCS1v15(p.rsaKey, crypto.SHA256, hash[:], signature); err != nil {
			return fmt.Errorf("%w: RSA signature verification failed", ErrTokenInvalid)
		}
		return nil

	case "ES256":
		if p.ecdsaKey == nil {
			return fmt.Errorf("%w: ECDSA key not configured", ErrTokenInvalid)
		}
		hash := sha256.Sum256(inputBytes)
		if !ecdsa.VerifyASN1(p.ecdsaKey, hash[:], signature) {
			return fmt.Errorf("%w: ECDSA signature verification failed", ErrTokenInvalid)
		}
		return nil

	default:
		return fmt.Errorf("%w: unsupported algorithm %s", ErrTokenInvalid, algorithm)
	}
}

// validateClaims validates JWT claims
func (p *JWTParser) validateClaims(claims *JWTClaims) error {
	now := time.Now()

	// Check expiration
	if claims.ExpiresAt > 0 {
		expiresAt := time.Unix(claims.ExpiresAt, 0)
		// Add clock skew tolerance
		if now.After(expiresAt.Add(p.config.JWTClockSkew)) {
			return ErrTokenExpired
		}
	}

	// Check not before
	if claims.NotBefore > 0 {
		notBefore := time.Unix(claims.NotBefore, 0)
		// Subtract clock skew tolerance
		if now.Before(notBefore.Add(-p.config.JWTClockSkew)) {
			return fmt.Errorf("%w: token not yet valid", ErrTokenInvalid)
		}
	}

	// Check issuer
	if p.config.JWTIssuer != "" && claims.Issuer != p.config.JWTIssuer {
		return fmt.Errorf("%w: invalid issuer", ErrTokenInvalid)
	}

	// Check audience
	if len(p.config.JWTAudience) > 0 {
		found := false
		for _, expected := range p.config.JWTAudience {
			for _, actual := range claims.Audience {
				if actual == expected {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: invalid audience", ErrTokenInvalid)
		}
	}

	// Check required claims
	for _, claim := range p.config.JWTRequiredClaims {
		switch claim {
		case "sub":
			if claims.Subject == "" {
				return fmt.Errorf("%w: missing required claim 'sub'", ErrTokenInvalid)
			}
		case "iss":
			if claims.Issuer == "" {
				return fmt.Errorf("%w: missing required claim 'iss'", ErrTokenInvalid)
			}
		case "aud":
			if len(claims.Audience) == 0 {
				return fmt.Errorf("%w: missing required claim 'aud'", ErrTokenInvalid)
			}
		case "exp":
			if claims.ExpiresAt == 0 {
				return fmt.Errorf("%w: missing required claim 'exp'", ErrTokenInvalid)
			}
		}
	}

	return nil
}

// GenerateToken generates a JWT token for a principal (for testing/internal use)
func (p *JWTParser) GenerateToken(principal *Principal, expiresIn time.Duration) (string, error) {
	if p.hmacKey == nil {
		return "", errors.New("token generation requires HMAC key")
	}

	now := time.Now()

	claims := JWTClaims{
		Subject:     principal.ID,
		Issuer:      p.config.JWTIssuer,
		Audience:    p.config.JWTAudience,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(expiresIn).Unix(),
		Roles:       principal.Roles,
		Permissions: principal.Permissions,
		Type:        string(principal.Type),
		Metadata:    principal.Metadata,
	}

	header := JWTHeader{
		Algorithm: "HS256",
		Type:      "JWT",
	}

	// Encode header
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerBytes)

	// Encode claims
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}
	claimsEncoded := base64.RawURLEncoding.EncodeToString(claimsBytes)

	// Create signature
	signatureInput := headerEncoded + "." + claimsEncoded
	mac := hmac.New(sha256.New, p.hmacKey)
	mac.Write([]byte(signatureInput))
	signature := mac.Sum(nil)
	signatureEncoded := base64.RawURLEncoding.EncodeToString(signature)

	return signatureInput + "." + signatureEncoded, nil
}
