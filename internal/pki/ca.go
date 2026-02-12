// Package pki provides a lightweight certificate authority for issuing
// short-lived device certificates in the Nexus mTLS workflow.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// CA is a lightweight certificate authority that issues device certificates
// signed by a CA key pair loaded from disk.
type CA struct {
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPEM  []byte
	validity time.Duration // default certificate lifetime
}

// LoadCA loads a CA certificate and private key from PEM files.
// validity sets the default lifetime for issued certificates.
func LoadCA(certFile, keyFile string, validity time.Duration) (*CA, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA key: %w", err)
	}

	return ParseCA(certPEM, keyPEM, validity)
}

// ParseCA creates a CA from PEM-encoded certificate and key bytes.
func ParseCA(certPEM, keyPEM []byte, validity time.Duration) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	if !cert.IsCA {
		return nil, fmt.Errorf("certificate is not a CA")
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	if validity <= 0 {
		validity = 24 * time.Hour
	}

	return &CA{
		cert:     cert,
		key:      key,
		certPEM:  certPEM,
		validity: validity,
	}, nil
}

// CACertPEM returns the CA certificate in PEM format.
func (ca *CA) CACertPEM() []byte {
	return ca.certPEM
}

// DeviceCert holds the output of IssueCertificate.
type DeviceCert struct {
	CertPEM []byte    // PEM-encoded device certificate
	KeyPEM  []byte    // PEM-encoded device private key
	Serial  *big.Int  // Certificate serial number
	Expiry  time.Time // Certificate expiration time
}

// IssueCertificate creates a new short-lived certificate for a device.
// The commonName is typically the device serial number.
func (ca *CA) IssueCertificate(commonName string) (*DeviceCert, error) {
	return ca.IssueCertificateWithValidity(commonName, ca.validity)
}

// IssueCertificateWithValidity creates a certificate with a specific lifetime.
func (ca *CA) IssueCertificateWithValidity(commonName string, validity time.Duration) (*DeviceCert, error) {
	// Generate device key pair
	deviceKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate device key: %w", err)
	}

	// Generate random serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	now := time.Now()
	expiry := now.Add(validity)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Nexus Device"},
		},
		NotBefore: now.Add(-1 * time.Minute), // small clock skew allowance
		NotAfter:  expiry,
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &deviceKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(deviceKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal device key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return &DeviceCert{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Serial:  serialNumber,
		Expiry:  expiry,
	}, nil
}

// GenerateCA creates a new self-signed CA certificate and key pair.
// This is a helper for testing and initial setup.
func GenerateCA(commonName string, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Nexus"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal CA key: %w", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return certPEM, keyPEM, nil
}
