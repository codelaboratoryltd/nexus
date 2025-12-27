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
	ring       *hashring.VirtualHashRing
	poolStore  store.PoolStore
	nodeStore  store.NodeStore
	allocStore store.AllocationStore
}

// NewServer creates a new API server.
func NewServer(ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore) *Server {
	return &Server{
		ring:       ring,
		poolStore:  poolStore,
		nodeStore:  nodeStore,
		allocStore: allocStore,
	}
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

	// Allocations
	api.HandleFunc("/allocations", s.listAllocations).Methods("GET")
	api.HandleFunc("/allocations", s.createAllocation).Methods("POST")
	api.HandleFunc("/allocations/{subscriber_id}", s.getAllocation).Methods("GET")
	api.HandleFunc("/allocations/{subscriber_id}", s.deleteAllocation).Methods("DELETE")
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
