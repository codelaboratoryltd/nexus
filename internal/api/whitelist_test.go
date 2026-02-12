package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/auth"
)

func setupWhitelistTestServer(t *testing.T) (*Server, *mux.Router) {
	t.Helper()

	whitelist, err := auth.NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	server := &Server{
		whitelist: whitelist,
	}

	router := mux.NewRouter()
	api := router.PathPrefix("/api/v1").Subrouter()
	api.Use(maxBytesMiddleware)
	api.HandleFunc("/devices/whitelist", server.addToWhitelist).Methods("POST")
	api.HandleFunc("/devices/pending", server.listPendingDevices).Methods("GET")
	api.HandleFunc("/devices/{id}/approve", server.approveDevice).Methods("POST")
	api.HandleFunc("/devices/{id}/revoke", server.revokeDevice).Methods("POST")

	return server, router
}

func TestWhitelistAPI_AddDevice(t *testing.T) {
	_, router := setupWhitelistTestServer(t)

	body := `{"serial":"SN001"}`
	req := httptest.NewRequest("POST", "/api/v1/devices/whitelist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var entry auth.WhitelistEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Serial != "SN001" {
		t.Errorf("expected serial SN001, got %s", entry.Serial)
	}
	if entry.Status != auth.DeviceStatusPending {
		t.Errorf("expected pending, got %s", entry.Status)
	}
}

func TestWhitelistAPI_AddDuplicate(t *testing.T) {
	_, router := setupWhitelistTestServer(t)

	body := `{"serial":"SN001"}`

	// First add
	req := httptest.NewRequest("POST", "/api/v1/devices/whitelist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("first add: expected 201, got %d", rec.Code)
	}

	// Second add (duplicate)
	req = httptest.NewRequest("POST", "/api/v1/devices/whitelist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate add: expected 409, got %d", rec.Code)
	}
}

func TestWhitelistAPI_AddMissingSerial(t *testing.T) {
	_, router := setupWhitelistTestServer(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/v1/devices/whitelist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestWhitelistAPI_ListPending(t *testing.T) {
	server, router := setupWhitelistTestServer(t)

	// Add some devices
	server.whitelist.Add("SN001")
	server.whitelist.Add("SN002")
	server.whitelist.Add("SN003")
	server.whitelist.Approve("SN002")

	req := httptest.NewRequest("GET", "/api/v1/devices/pending", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var result struct {
		Devices []auth.WhitelistEntry `json:"devices"`
		Count   int                   `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Count != 2 {
		t.Errorf("expected 2 pending, got %d", result.Count)
	}
}

func TestWhitelistAPI_ApproveDevice(t *testing.T) {
	server, router := setupWhitelistTestServer(t)

	server.whitelist.Add("SN001")

	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var entry auth.WhitelistEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Status != auth.DeviceStatusApproved {
		t.Errorf("expected approved, got %s", entry.Status)
	}
}

func TestWhitelistAPI_ApproveNonExistent(t *testing.T) {
	_, router := setupWhitelistTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/devices/SN999/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestWhitelistAPI_RevokeDevice(t *testing.T) {
	server, router := setupWhitelistTestServer(t)

	server.whitelist.Add("SN001")
	server.whitelist.Approve("SN001")

	req := httptest.NewRequest("POST", "/api/v1/devices/SN001/revoke", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var entry auth.WhitelistEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Status != auth.DeviceStatusRevoked {
		t.Errorf("expected revoked, got %s", entry.Status)
	}
}

func TestWhitelistAPI_RevokeNonExistent(t *testing.T) {
	_, router := setupWhitelistTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/devices/SN999/revoke", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
