package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// WhitelistAddRequest represents a request to add a device to the whitelist.
type WhitelistAddRequest struct {
	Serial string `json:"serial"`
}

// addToWhitelist adds a device serial to the whitelist.
// POST /api/v1/devices/whitelist
func (s *Server) addToWhitelist(w http.ResponseWriter, r *http.Request) {
	var req WhitelistAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Serial == "" {
		respondError(w, http.StatusBadRequest, "serial is required")
		return
	}

	if err := s.whitelist.Add(req.Serial); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}

	entry := s.whitelist.Get(req.Serial)
	respondJSON(w, http.StatusCreated, entry)
}

// listPendingDevices returns all devices awaiting approval.
// GET /api/v1/devices/pending
func (s *Server) listPendingDevices(w http.ResponseWriter, r *http.Request) {
	pending := s.whitelist.ListPending()
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"devices": pending,
		"count":   len(pending),
	})
}

// approveDevice approves a pending device.
// POST /api/v1/devices/{id}/approve
func (s *Server) approveDevice(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serial := vars["id"]

	if serial == "" {
		respondError(w, http.StatusBadRequest, "device id is required")
		return
	}

	if err := s.whitelist.Approve(serial); err != nil {
		entry := s.whitelist.Get(serial)
		if entry == nil {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusConflict, err.Error())
		}
		return
	}

	entry := s.whitelist.Get(serial)
	respondJSON(w, http.StatusOK, entry)
}

// revokeDevice revokes device access.
// POST /api/v1/devices/{id}/revoke
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serial := vars["id"]

	if serial == "" {
		respondError(w, http.StatusBadRequest, "device id is required")
		return
	}

	if err := s.whitelist.Revoke(serial); err != nil {
		entry := s.whitelist.Get(serial)
		if entry == nil {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusConflict, err.Error())
		}
		return
	}

	entry := s.whitelist.Get(serial)
	respondJSON(w, http.StatusOK, entry)
}
