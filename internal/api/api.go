package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/auth"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/state"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// maxRequestBodySize is the maximum allowed request body size (1 MB).
const maxRequestBodySize = 1 << 20

// ReadinessChecker provides peer discovery and CRDT sync status for the ready endpoint.
type ReadinessChecker interface {
	IsPeerDiscoveryReady() bool
	ConnectedPeerCount() int
	CRDTSyncStatus() state.CRDTSyncStatus
	IsCRDTSyncHealthy() bool
}

// Server provides the HTTP API for Nexus.
type Server struct {
	ring             *hashring.VirtualHashRing
	poolStore        store.PoolStore
	nodeStore        store.NodeStore
	allocStore       store.AllocationStore
	deviceStore      store.DeviceStore
	readinessChecker ReadinessChecker
	configWatcher    ConfigWatcher
	whitelist        *auth.DeviceWhitelist
}

// NewServer creates a new API server.
func NewServer(ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore) *Server {
	return &Server{
		ring:             ring,
		poolStore:        poolStore,
		nodeStore:        nodeStore,
		allocStore:       allocStore,
		deviceStore:      nil, // Use SetDeviceStore to enable bootstrap API
		readinessChecker: nil, // Use SetReadinessChecker to enable peer discovery readiness
	}
}

// NewServerWithDevices creates a new API server with device store support.
func NewServerWithDevices(ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore, deviceStore store.DeviceStore) *Server {
	return &Server{
		ring:             ring,
		poolStore:        poolStore,
		nodeStore:        nodeStore,
		allocStore:       allocStore,
		deviceStore:      deviceStore,
		readinessChecker: nil,
	}
}

// SetReadinessChecker sets the readiness checker for peer discovery status.
func (s *Server) SetReadinessChecker(checker ReadinessChecker) {
	s.readinessChecker = checker
}

// SetDeviceStore sets the device store, enabling the bootstrap API.
func (s *Server) SetDeviceStore(deviceStore store.DeviceStore) {
	s.deviceStore = deviceStore
}

// SetWhitelist sets the device whitelist, enabling whitelist API endpoints.
func (s *Server) SetWhitelist(whitelist *auth.DeviceWhitelist) {
	s.whitelist = whitelist
}

// maxBytesMiddleware limits request body size to prevent oversized payloads.
func maxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		next.ServeHTTP(w, r)
	})
}

// isMaxBytesError returns true if the error is caused by exceeding the body size limit.
func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

// RegisterRoutes registers all API routes on the given router.
func (s *Server) RegisterRoutes(r *mux.Router) {
	// Health endpoints
	r.HandleFunc("/health", s.healthHandler).Methods("GET")
	r.HandleFunc("/ready", s.readyHandler).Methods("GET")

	// API v1 endpoints
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(maxBytesMiddleware)

	// Pools
	api.HandleFunc("/pools", s.listPools).Methods("GET")
	api.HandleFunc("/pools", s.createPool).Methods("POST")
	api.HandleFunc("/pools/{id}", s.getPool).Methods("GET")
	api.HandleFunc("/pools/{id}", s.deletePool).Methods("DELETE")

	// Nodes
	api.HandleFunc("/nodes", s.listNodes).Methods("GET")
	api.HandleFunc("/nodes/{node_id}/allocations", s.listNodeAllocations).Methods("GET")
	api.HandleFunc("/nodes/{node_id}/backup-allocations", s.listBackupAllocations).Methods("GET")

	// Allocations
	api.HandleFunc("/allocations", s.listAllocations).Methods("GET")
	api.HandleFunc("/allocations", s.createAllocation).Methods("POST")
	api.HandleFunc("/allocations/expiring", s.listExpiringAllocations).Methods("GET")
	api.HandleFunc("/allocations/{subscriber_id}", s.getAllocation).Methods("GET")
	api.HandleFunc("/allocations/{subscriber_id}", s.deleteAllocation).Methods("DELETE")
	api.HandleFunc("/allocations/{subscriber_id}/renew", s.renewAllocation).Methods("POST")

	// Backup Allocations
	api.HandleFunc("/pools/{pool_id}/backup-allocations", s.createBackupAllocations).Methods("POST")

	// Config watch streaming (SSE)
	api.HandleFunc("/watch", s.watchHandler).Methods("GET")

	// Bootstrap (device registration)
	if s.deviceStore != nil {
		api.HandleFunc("/bootstrap", s.bootstrap).Methods("POST")
		api.HandleFunc("/devices", s.listDevices).Methods("GET")
		api.HandleFunc("/devices/import", s.importDevices).Methods("POST")
		api.HandleFunc("/devices/{node_id}", s.getDevice).Methods("GET")
		api.HandleFunc("/devices/{node_id}", s.assignDevice).Methods("PUT")
		api.HandleFunc("/devices/{node_id}", s.deleteDevice).Methods("DELETE")
	}

	// Device whitelist (approval workflow)
	if s.whitelist != nil {
		api.HandleFunc("/devices/whitelist", s.addToWhitelist).Methods("POST")
		api.HandleFunc("/devices/pending", s.listPendingDevices).Methods("GET")
		api.HandleFunc("/devices/{id}/approve", s.approveDevice).Methods("POST")
		api.HandleFunc("/devices/{id}/revoke", s.revokeDevice).Methods("POST")
	}
}

// respondJSON writes a JSON response.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// respondError writes an error response.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// healthHandler returns health status.
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// readyResponse is the JSON response for the /ready endpoint.
type readyResponse struct {
	Ready    bool              `json:"ready"`
	CRDTSync *crdtSyncResponse `json:"crdt_sync,omitempty"`
}

// crdtSyncResponse represents the CRDT sync status in the ready response.
type crdtSyncResponse struct {
	PeersConnected int    `json:"peers_connected"`
	SyncLagMs      int64  `json:"sync_lag_ms"`
	LastSync       string `json:"last_sync"`
}

// readyHandler returns readiness status.
// If a readiness checker is configured (P2P mode with DNS discovery),
// this will return 503 until peer discovery is ready.
func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	// If no readiness checker is configured, always return ready (standalone mode)
	if s.readinessChecker == nil {
		respondJSON(w, http.StatusOK, readyResponse{Ready: true})
		return
	}

	discoveryReady := s.readinessChecker.IsPeerDiscoveryReady()
	syncHealthy := s.readinessChecker.IsCRDTSyncHealthy()
	syncStatus := s.readinessChecker.CRDTSyncStatus()

	ready := discoveryReady && syncHealthy

	resp := readyResponse{
		Ready: ready,
		CRDTSync: &crdtSyncResponse{
			PeersConnected: syncStatus.PeersConnected,
			SyncLagMs:      syncStatus.SyncLagMs,
		},
	}

	if !syncStatus.LastSync.IsZero() {
		resp.CRDTSync.LastSync = syncStatus.LastSync.Format(time.RFC3339)
	}

	if ready {
		respondJSON(w, http.StatusOK, resp)
	} else {
		respondJSON(w, http.StatusServiceUnavailable, resp)
	}
}
