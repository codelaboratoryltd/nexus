package pki

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateCA("Test CA", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	if len(certPEM) == 0 {
		t.Error("certPEM is empty")
	}
	if len(keyPEM) == 0 {
		t.Error("keyPEM is empty")
	}

	// Parse and verify the CA cert
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode certPEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	if !cert.IsCA {
		t.Error("certificate should be a CA")
	}
	if cert.Subject.CommonName != "Test CA" {
		t.Errorf("expected CN 'Test CA', got '%s'", cert.Subject.CommonName)
	}
}

func TestParseCA(t *testing.T) {
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)

	ca, err := ParseCA(certPEM, keyPEM, 1*time.Hour)
	if err != nil {
		t.Fatalf("ParseCA: %v", err)
	}

	if ca.validity != 1*time.Hour {
		t.Errorf("expected validity 1h, got %v", ca.validity)
	}

	if len(ca.CACertPEM()) == 0 {
		t.Error("CACertPEM is empty")
	}
}

func TestParseCA_DefaultValidity(t *testing.T) {
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)

	ca, err := ParseCA(certPEM, keyPEM, 0)
	if err != nil {
		t.Fatalf("ParseCA: %v", err)
	}

	if ca.validity != 24*time.Hour {
		t.Errorf("expected default validity 24h, got %v", ca.validity)
	}
}

func TestParseCA_InvalidCert(t *testing.T) {
	_, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)

	_, err := ParseCA([]byte("not pem"), keyPEM, time.Hour)
	if err == nil {
		t.Error("should error on invalid cert PEM")
	}
}

func TestParseCA_InvalidKey(t *testing.T) {
	certPEM, _, _ := GenerateCA("Test CA", 24*time.Hour)

	_, err := ParseCA(certPEM, []byte("not pem"), time.Hour)
	if err == nil {
		t.Error("should error on invalid key PEM")
	}
}

func TestParseCA_NotCA(t *testing.T) {
	// Generate a CA to sign a non-CA cert
	caCertPEM, caKeyPEM, _ := GenerateCA("Real CA", 24*time.Hour)
	ca, _ := ParseCA(caCertPEM, caKeyPEM, time.Hour)

	// Issue a device cert (not a CA)
	deviceCert, _ := ca.IssueCertificate("device-1")

	// Try to use device cert as CA
	_, err := ParseCA(deviceCert.CertPEM, deviceCert.KeyPEM, time.Hour)
	if err == nil {
		t.Error("should error when cert is not a CA")
	}
}

func TestLoadCA(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)

	certFile := filepath.Join(dir, "ca.pem")
	keyFile := filepath.Join(dir, "ca-key.pem")
	os.WriteFile(certFile, certPEM, 0600)
	os.WriteFile(keyFile, keyPEM, 0600)

	ca, err := LoadCA(certFile, keyFile, time.Hour)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	if ca == nil {
		t.Fatal("CA is nil")
	}
}

func TestLoadCA_MissingFiles(t *testing.T) {
	_, err := LoadCA("/nonexistent/cert.pem", "/nonexistent/key.pem", time.Hour)
	if err == nil {
		t.Error("should error on missing files")
	}
}

func TestIssueCertificate(t *testing.T) {
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)
	ca, _ := ParseCA(certPEM, keyPEM, 1*time.Hour)

	device, err := ca.IssueCertificate("device-SN001")
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}

	if len(device.CertPEM) == 0 {
		t.Error("CertPEM is empty")
	}
	if len(device.KeyPEM) == 0 {
		t.Error("KeyPEM is empty")
	}
	if device.Serial == nil {
		t.Error("Serial is nil")
	}

	// Verify the device cert
	block, _ := pem.Decode(device.CertPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	if cert.Subject.CommonName != "device-SN001" {
		t.Errorf("expected CN 'device-SN001', got '%s'", cert.Subject.CommonName)
	}
	if cert.IsCA {
		t.Error("device cert should not be a CA")
	}

	// Check key usage
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("should have DigitalSignature key usage")
	}

	hasClientAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasClientAuth {
		t.Error("should have ClientAuth extended key usage")
	}

	// Verify cert is signed by CA
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(certPEM)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     caPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("cert verification failed: %v", err)
	}

	// Check expiry is within expected range
	expectedExpiry := time.Now().Add(1 * time.Hour)
	if device.Expiry.Sub(expectedExpiry) > 5*time.Second {
		t.Errorf("expiry too far from expected: %v", device.Expiry)
	}
}

func TestIssueCertificateWithValidity(t *testing.T) {
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)
	ca, _ := ParseCA(certPEM, keyPEM, 1*time.Hour)

	device, err := ca.IssueCertificateWithValidity("device-SN002", 30*time.Minute)
	if err != nil {
		t.Fatalf("IssueCertificateWithValidity: %v", err)
	}

	expectedExpiry := time.Now().Add(30 * time.Minute)
	if device.Expiry.Sub(expectedExpiry) > 5*time.Second {
		t.Errorf("expiry should be ~30m from now, got %v", device.Expiry)
	}
}

func TestIssueCertificate_UniqueSerials(t *testing.T) {
	certPEM, keyPEM, _ := GenerateCA("Test CA", 24*time.Hour)
	ca, _ := ParseCA(certPEM, keyPEM, 1*time.Hour)

	cert1, _ := ca.IssueCertificate("device-1")
	cert2, _ := ca.IssueCertificate("device-2")

	if cert1.Serial.Cmp(cert2.Serial) == 0 {
		t.Error("certificates should have unique serial numbers")
	}
}
