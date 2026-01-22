package api

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/validation"
)

// AllocationRequest represents an allocation creation request.
type AllocationRequest struct {
	PoolID       string `json:"pool_id"`
	SubscriberID string `json:"subscriber_id"`
	IP           string `json:"ip,omitempty"` // Optional: specify IP or let system allocate
}

// AllocationResponse represents an allocation in API responses.
type AllocationResponse struct {
	PoolID       string    `json:"pool_id"`
	SubscriberID string    `json:"subscriber_id"`
	IP           string    `json:"ip"`
	Timestamp    time.Time `json:"timestamp"`
}

// listAllocations returns allocations, optionally filtered by pool.
func (s *Server) listAllocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	poolID := r.URL.Query().Get("pool_id")

	if poolID == "" {
		respondError(w, http.StatusBadRequest, "pool_id query parameter is required")
		return
	}

	// Validate pool ID from query parameter
	if err := validation.ValidatePoolID(poolID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	allocations, err := s.allocStore.ListAllocationsByPool(ctx, poolID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]AllocationResponse, 0, len(allocations))
	for _, a := range allocations {
		response = append(response, allocationToResponse(a))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"allocations": response,
		"count":       len(response),
	})
}

// createAllocation creates a new IP allocation.
func (s *Server) createAllocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req AllocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: malformed JSON")
		return
	}

	// Validate pool ID
	if err := validation.ValidatePoolID(req.PoolID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate subscriber ID
	if err := validation.ValidateSubscriberID(req.SubscriberID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check if subscriber already has an allocation
	existing, err := s.allocStore.GetAllocationBySubscriber(ctx, req.SubscriberID)
	if err == nil && existing != nil {
		respondError(w, http.StatusConflict, "subscriber already has an allocation")
		return
	}

	var ip net.IP
	if req.IP != "" {
		// Validate and parse IP address
		parsedIP, err := validation.ValidateIPAddress(req.IP)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		ip = parsedIP
	} else {
		// Allocate from hashring based on subscriber ID
		allocatedIP := s.ring.AllocateIP(req.PoolID, req.SubscriberID)
		if allocatedIP == nil {
			respondError(w, http.StatusServiceUnavailable, "no IPs available in pool")
			return
		}
		ip = allocatedIP
	}

	allocation := &store.Allocation{
		PoolID:       req.PoolID,
		SubscriberID: req.SubscriberID,
		IP:           ip,
		Timestamp:    time.Now(),
	}

	if err := s.allocStore.SaveAllocation(ctx, allocation); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, allocationToResponse(allocation))
}

// getAllocation returns a subscriber's allocation.
func (s *Server) getAllocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	subscriberID := vars["subscriber_id"]

	// Validate subscriber ID from URL
	if err := validation.ValidateSubscriberID(subscriberID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	allocation, err := s.allocStore.GetAllocationBySubscriber(ctx, subscriberID)
	if err != nil {
		respondError(w, http.StatusNotFound, "allocation not found")
		return
	}

	respondJSON(w, http.StatusOK, allocationToResponse(allocation))
}

// deleteAllocation removes a subscriber's allocation.
func (s *Server) deleteAllocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	subscriberID := vars["subscriber_id"]
	poolID := r.URL.Query().Get("pool_id")

	// Validate subscriber ID from URL
	if err := validation.ValidateSubscriberID(subscriberID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate pool ID if provided
	if poolID != "" {
		if err := validation.ValidatePoolID(poolID); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if poolID == "" {
		// Try to find the allocation first
		allocation, err := s.allocStore.GetAllocationBySubscriber(ctx, subscriberID)
		if err != nil {
			respondError(w, http.StatusNotFound, "allocation not found")
			return
		}
		poolID = allocation.PoolID
	}

	if err := s.allocStore.RemoveAllocation(ctx, poolID, subscriberID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// allocationToResponse converts a store.Allocation to API response.
func allocationToResponse(a *store.Allocation) AllocationResponse {
	return AllocationResponse{
		PoolID:       a.PoolID,
		SubscriberID: a.SubscriberID,
		IP:           a.IP.String(),
		Timestamp:    a.Timestamp,
	}
}
