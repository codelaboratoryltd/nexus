package api

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/store"
)

// PoolRequest represents a pool creation request.
type PoolRequest struct {
	ID             string            `json:"id"`
	CIDR           string            `json:"cidr"`
	Prefix         int               `json:"prefix"`
	Exclusions     []string          `json:"exclusions"`
	Metadata       map[string]string `json:"metadata"`
	ShardingFactor int               `json:"sharding_factor"`
}

// PoolResponse represents a pool in API responses.
type PoolResponse struct {
	ID             string            `json:"id"`
	CIDR           string            `json:"cidr"`
	Prefix         int               `json:"prefix"`
	Exclusions     []string          `json:"exclusions"`
	Metadata       map[string]string `json:"metadata"`
	ShardingFactor int               `json:"sharding_factor"`
}

// listPools returns all pools.
func (s *Server) listPools(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pools, err := s.poolStore.ListPools(ctx)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]PoolResponse, 0, len(pools))
	for _, p := range pools {
		response = append(response, poolToResponse(p))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"pools": response,
		"count": len(response),
	})
}

// createPool creates a new pool.
func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req PoolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ID == "" {
		respondError(w, http.StatusBadRequest, "pool id is required")
		return
	}

	if req.CIDR == "" {
		respondError(w, http.StatusBadRequest, "cidr is required")
		return
	}

	_, ipNet, err := net.ParseCIDR(req.CIDR)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid cidr format")
		return
	}

	pool := &store.Pool{
		ID:             req.ID,
		CIDR:           *ipNet,
		Prefix:         req.Prefix,
		Exclusions:     req.Exclusions,
		Metadata:       req.Metadata,
		ShardingFactor: req.ShardingFactor,
	}

	if err := pool.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.poolStore.SavePool(ctx, pool); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Also add to hashring for immediate use
	s.ring.AddPool(req.ID, ipNet)

	respondJSON(w, http.StatusCreated, poolToResponse(pool))
}

// getPool returns a specific pool.
func (s *Server) getPool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	poolID := vars["id"]

	pool, err := s.poolStore.GetPool(ctx, poolID)
	if err != nil {
		respondError(w, http.StatusNotFound, "pool not found")
		return
	}

	respondJSON(w, http.StatusOK, poolToResponse(pool))
}

// deletePool removes a pool.
func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	poolID := vars["id"]

	if err := s.poolStore.DeletePool(ctx, poolID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Also remove from hashring
	s.ring.RemovePool(poolID)

	w.WriteHeader(http.StatusNoContent)
}

// poolToResponse converts a store.Pool to API response.
func poolToResponse(p *store.Pool) PoolResponse {
	return PoolResponse{
		ID:             p.ID,
		CIDR:           p.CIDR.String(),
		Prefix:         p.Prefix,
		Exclusions:     p.Exclusions,
		Metadata:       p.Metadata,
		ShardingFactor: p.ShardingFactor,
	}
}
