package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTLSConfig_Enabled(t *testing.T) {
	tests := []struct {
		name    string
		cert    string
		key     string
		enabled bool
	}{
		{"both set", "cert.pem", "key.pem", true},
		{"only cert", "cert.pem", "", false},
		{"only key", "", "key.pem", false},
		{"neither", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &TLSConfig{CertFile: tt.cert, KeyFile: tt.key}
			if cfg.Enabled() != tt.enabled {
				t.Errorf("Enabled() = %v, want %v", cfg.Enabled(), tt.enabled)
			}
		})
	}
}

func TestTLSConfig_BuildTLSConfig(t *testing.T) {
	dir := t.TempDir()

	// Generate a test CA
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	caCertDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	caFile := filepath.Join(dir, "ca.pem")
	os.WriteFile(caFile, caCertPEM, 0600)

	t.Run("not enabled", func(t *testing.T) {
		cfg := &TLSConfig{}
		_, err := cfg.BuildTLSConfig()
		if err == nil {
			t.Error("should error when not enabled")
		}
	})

	t.Run("with CA and client auth", func(t *testing.T) {
		cfg := &TLSConfig{
			CertFile:   "server.pem",
			KeyFile:    "server-key.pem",
			CAFile:     caFile,
			ClientAuth: true,
		}
		tlsCfg, err := cfg.BuildTLSConfig()
		if err != nil {
			t.Fatalf("BuildTLSConfig: %v", err)
		}
		if tlsCfg.ClientAuth != tls.VerifyClientCertIfGiven {
			t.Errorf("expected VerifyClientCertIfGiven, got %v", tlsCfg.ClientAuth)
		}
		if tlsCfg.ClientCAs == nil {
			t.Error("ClientCAs should be set")
		}
		if tlsCfg.MinVersion != tls.VersionTLS12 {
			t.Error("MinVersion should be TLS 1.2")
		}
	})

	t.Run("invalid CA file", func(t *testing.T) {
		cfg := &TLSConfig{
			CertFile: "server.pem",
			KeyFile:  "server-key.pem",
			CAFile:   "/nonexistent/ca.pem",
		}
		_, err := cfg.BuildTLSConfig()
		if err == nil {
			t.Error("should error with invalid CA file")
		}
	})

	t.Run("invalid CA content", func(t *testing.T) {
		badCA := filepath.Join(dir, "bad-ca.pem")
		os.WriteFile(badCA, []byte("not a cert"), 0600)

		cfg := &TLSConfig{
			CertFile: "server.pem",
			KeyFile:  "server-key.pem",
			CAFile:   badCA,
		}
		_, err := cfg.BuildTLSConfig()
		if err == nil {
			t.Error("should error with invalid CA content")
		}
	})
}

func TestMTLSMiddleware_ExemptPaths(t *testing.T) {
	m := NewMTLSMiddleware([]string{"/health", "/api/v1/bootstrap"})

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		path       string
		hasTLS     bool
		hasCert    bool
		wantStatus int
	}{
		{"/health", false, false, http.StatusOK},
		{"/api/v1/bootstrap", false, false, http.StatusOK},
		{"/api/v1/pools", false, false, http.StatusUnauthorized},
		{"/api/v1/pools", true, false, http.StatusUnauthorized},
		{"/api/v1/pools", true, true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)

			if tt.hasTLS {
				req.TLS = &tls.ConnectionState{}
				if tt.hasCert {
					req.TLS.PeerCertificates = []*x509.Certificate{{}}
				}
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("path %s: expected %d, got %d", tt.path, tt.wantStatus, rec.Code)
			}
		})
	}
}
