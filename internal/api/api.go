package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// Server provides the HTTP API for Nexus.
type Server struct {
	ring        *hashring.VirtualHashRing
	poolStore   store.PoolStore
	nodeStore   store.NodeStore
	allocStore  store.AllocationStore
	deviceStore store.DeviceStore
}

// NewServer creates a new API server.
func NewServer(ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore) *Server {
	return &Server{
		ring:        ring,
		poolStore:   poolStore,
		nodeStore:   nodeStore,
		allocStore:  allocStore,
		deviceStore: nil, // Use SetDeviceStore to enable bootstrap API
	}
}

// NewServerWithDevices creates a new API server with device store support.
func NewServerWithDevices(ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore, deviceStore store.DeviceStore) *Server {
	return &Server{
		ring:        ring,
		poolStore:   poolStore,
		nodeStore:   nodeStore,
		allocStore:  allocStore,
		deviceStore: deviceStore,
	}
}

// SetDeviceStore sets the device store, enabling the bootstrap API.
func (s *Server) SetDeviceStore(deviceStore store.DeviceStore) {
	s.deviceStore = deviceStore
}

// RegisterRoutes registers all API routes on the given router.
func (s *Server) RegisterRoutes(r *mux.Router) {
	// Health endpoints
	r.HandleFunc("/health", s.healthHandler).Methods("GET")
	r.HandleFunc("/ready", s.readyHandler).Methods("GET")

	// API v1 endpoints
	api := r.PathPrefix("/api/v1").Subrouter()

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
	api.HandleFunc("/allocations/{subscriber_id}", s.getAllocation).Methods("GET")
	api.HandleFunc("/allocations/{subscriber_id}", s.deleteAllocation).Methods("DELETE")

	// Backup Allocations
	api.HandleFunc("/pools/{pool_id}/backup-allocations", s.createBackupAllocations).Methods("POST")

	// Bootstrap (device registration)
	if s.deviceStore != nil {
		api.HandleFunc("/bootstrap", s.bootstrap).Methods("POST")
		api.HandleFunc("/devices", s.listDevices).Methods("GET")
		api.HandleFunc("/devices/import", s.importDevices).Methods("POST")
		api.HandleFunc("/devices/{node_id}", s.getDevice).Methods("GET")
		api.HandleFunc("/devices/{node_id}", s.assignDevice).Methods("PUT")
		api.HandleFunc("/devices/{node_id}", s.deleteDevice).Methods("DELETE")
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

// readyHandler returns readiness status.
func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}
