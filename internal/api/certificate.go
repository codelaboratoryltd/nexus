package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/auth"
	"github.com/codelaboratoryltd/nexus/internal/pki"
)

// CertificateRequest allows optional validity override.
type CertificateRequest struct {
	ValidityHours int `json:"validity_hours,omitempty"`
}

// CertificateResponse is returned after issuing a device certificate.
type CertificateResponse struct {
	CertPEM  string    `json:"cert_pem"`
	KeyPEM   string    `json:"key_pem"`
	CaPEM    string    `json:"ca_pem"`
	Serial   string    `json:"serial"`
	Expiry   time.Time `json:"expiry"`
	DeviceID string    `json:"device_id"`
}

// issueCertificate issues a short-lived mTLS client certificate for an approved device.
// POST /api/v1/devices/{id}/certificate
func (s *Server) issueCertificate(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serial := vars["id"]

	if serial == "" {
		respondError(w, http.StatusBadRequest, "device id is required")
		return
	}

	// Check device is approved in whitelist
	entry := s.whitelist.Get(serial)
	if entry == nil {
		respondError(w, http.StatusNotFound, "device not found in whitelist")
		return
	}

	if entry.Status != auth.DeviceStatusApproved {
		respondError(w, http.StatusForbidden, "device is not approved (status: "+string(entry.Status)+")")
		return
	}

	// Parse optional validity override
	var req CertificateRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isMaxBytesError(err) {
				respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	var deviceCert *pki.DeviceCert
	var err error

	if req.ValidityHours > 0 {
		validity := time.Duration(req.ValidityHours) * time.Hour
		deviceCert, err = s.ca.IssueCertificateWithValidity(serial, validity)
	} else {
		deviceCert, err = s.ca.IssueCertificate(serial)
	}

	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to issue certificate: "+err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, CertificateResponse{
		CertPEM:  string(deviceCert.CertPEM),
		KeyPEM:   string(deviceCert.KeyPEM),
		CaPEM:    string(s.ca.CACertPEM()),
		Serial:   deviceCert.Serial.String(),
		Expiry:   deviceCert.Expiry,
		DeviceID: serial,
	})
}
