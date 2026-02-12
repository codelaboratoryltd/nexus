package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/auth"
	"github.com/codelaboratoryltd/nexus/internal/pki"
)

func setupCertTestServer(t *testing.T) (*Server, *mux.Router) {
	t.Helper()

	whitelist, err := auth.NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	certPEM, keyPEM, err := pki.GenerateCA("Test CA", 24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	ca, err := pki.ParseCA(certPEM, keyPEM, 1*time.Hour)
	if err != nil {
		t.Fatalf("ParseCA: %v", err)
	}

	server := &Server{
		whitelist: whitelist,
		ca:        ca,
	}

	router := mux.NewRouter()
	api := router.PathPrefix("/api/v1").Subrouter()
	api.Use(maxBytesMiddleware)
	api.HandleFunc("/devices/{id}/certificate", server.issueCertificate).Methods("POST")

	return server, router
}

func TestCertificateAPI_IssueCert(t *testing.T) {
	server, router := setupCertTestServer(t)

	// Add and approve device
	server.whitelist.Add("SN001")
	server.whitelist.Approve("SN001")

	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/certificate", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CertificateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.CertPEM == "" {
		t.Error("CertPEM is empty")
	}
	if resp.KeyPEM == "" {
		t.Error("KeyPEM is empty")
	}
	if resp.CaPEM == "" {
		t.Error("CaPEM is empty")
	}
	if resp.Serial == "" {
		t.Error("Serial is empty")
	}
	if resp.DeviceID != "SN001" {
		t.Errorf("expected DeviceID SN001, got %s", resp.DeviceID)
	}
	if resp.Expiry.IsZero() {
		t.Error("Expiry is zero")
	}
}

func TestCertificateAPI_IssueCertWithValidity(t *testing.T) {
	server, router := setupCertTestServer(t)

	server.whitelist.Add("SN001")
	server.whitelist.Approve("SN001")

	body := `{"validity_hours": 2}`
	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/certificate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CertificateResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	expectedExpiry := time.Now().Add(2 * time.Hour)
	if resp.Expiry.Sub(expectedExpiry) > 10*time.Second {
		t.Errorf("expiry too far from expected: got %v, want ~%v", resp.Expiry, expectedExpiry)
	}
}

func TestCertificateAPI_NotFound(t *testing.T) {
	_, router := setupCertTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/devices/SN999/certificate", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCertificateAPI_NotApproved(t *testing.T) {
	server, router := setupCertTestServer(t)

	// Add but don't approve
	server.whitelist.Add("SN001")

	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/certificate", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCertificateAPI_Revoked(t *testing.T) {
	server, router := setupCertTestServer(t)

	server.whitelist.Add("SN001")
	server.whitelist.Approve("SN001")
	server.whitelist.Revoke("SN001")

	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/certificate", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
