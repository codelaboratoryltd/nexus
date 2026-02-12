package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// TLSConfig holds mTLS server configuration.
type TLSConfig struct {
	CertFile   string // Path to server certificate
	KeyFile    string // Path to server private key
	CAFile     string // Path to CA certificate for client verification
	ClientAuth bool   // Require client certificates
}

// Enabled returns true if TLS is configured (cert and key provided).
func (c *TLSConfig) Enabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}

// BuildTLSConfig creates a *tls.Config from the TLSConfig.
// When ClientAuth is true and CAFile is set, clients must present
// a certificate signed by the CA.
func (c *TLSConfig) BuildTLSConfig() (*tls.Config, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("TLS not configured: cert and key are required")
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load CA for client verification
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caPool

		if c.ClientAuth {
			// VerifyClientCertIfGiven allows unauthenticated endpoints
			// (like /health) to work without client certs.
			// The mTLS middleware enforces certs on protected endpoints.
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}

	return tlsConfig, nil
}

// MTLSMiddleware enforces client certificate authentication on protected
// endpoints. Requests to exempt paths (e.g. /health, /api/v1/bootstrap)
// are passed through without verification.
type MTLSMiddleware struct {
	exemptPaths []string
}

// NewMTLSMiddleware creates middleware that requires client certs except
// on the given exempt paths.
func NewMTLSMiddleware(exemptPaths []string) *MTLSMiddleware {
	return &MTLSMiddleware{exemptPaths: exemptPaths}
}

// Handler returns an http.Handler middleware that enforces mTLS.
func (m *MTLSMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if path is exempt
		for _, path := range m.exemptPaths {
			if r.URL.Path == path || strings.HasPrefix(r.URL.Path, path+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Require client certificate for non-exempt paths
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, `{"error":"client certificate required"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
