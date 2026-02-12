package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/validation"
)

// AllocationRequest represents an allocation creation request.
type AllocationRequest struct {
	PoolID       string `json:"pool_id"`
	SubscriberID string `json:"subscriber_id"`
	IP           string `json:"ip,omitempty"`         // Optional: specify IP or let system allocate
	NodeID       string `json:"node_id,omitempty"`    // Optional: primary node that owns this allocation
	TTL          int64  `json:"ttl,omitempty"`        // Optional: TTL in seconds (0 = permanent)
	AllocType    string `json:"alloc_type,omitempty"` // Optional: "session", "sticky", "permanent"
}

// AllocationResponse represents an allocation in API responses.
type AllocationResponse struct {
	PoolID       string    `json:"pool_id"`
	SubscriberID string    `json:"subscriber_id"`
	IP           string    `json:"ip"`
	Timestamp    time.Time `json:"timestamp"`
	NodeID       string    `json:"node_id,omitempty"`
	BackupNodeID string    `json:"backup_node_id,omitempty"`
	IsBackup     bool      `json:"is_backup,omitempty"`
	TTL          int64     `json:"ttl,omitempty"`
	Epoch        uint64    `json:"epoch,omitempty"`      // Epoch when allocation was created/renewed
	ExpiresAt    time.Time `json:"expires_at,omitempty"` // Approximate expiration (informational)
	LastRenewed  time.Time `json:"last_renewed,omitempty"`
	AllocType    string    `json:"alloc_type,omitempty"`
}

// RenewAllocationRequest represents a request to renew an allocation's TTL.
type RenewAllocationRequest struct {
	TTL int64 `json:"ttl,omitempty"` // New TTL in seconds (0 = use original TTL)
}

// BackupAllocationRequest represents a request to create backup allocations.
type BackupAllocationRequest struct {
	BackupNodeID string `json:"backup_node_id"` // The standby node to assign backup allocations to
}

// BackupAllocationResponse represents a backup allocation assignment result.
type BackupAllocationResponse struct {
	PoolID           string `json:"pool_id"`
	BackupNodeID     string `json:"backup_node_id"`
	AllocationsCount int    `json:"allocations_count"`
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
		if isMaxBytesError(err) {
			respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
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

	// Validate node ID if provided
	if req.NodeID != "" {
		if err := validation.ValidateNodeID(req.NodeID); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
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

	now := time.Now()
	allocation := &store.Allocation{
		PoolID:       req.PoolID,
		SubscriberID: req.SubscriberID,
		IP:           ip,
		Timestamp:    now,
		NodeID:       req.NodeID,
	}

	// Set TTL fields if provided (epoch-based expiration)
	if req.TTL > 0 {
		allocation.TTL = req.TTL
		allocation.Epoch = s.allocStore.GetCurrentEpoch() // Set current epoch for expiration tracking
		allocation.LastRenewed = now
		// ExpiresAt is informational only - actual expiration is epoch-based
		epochPeriod := s.allocStore.GetEpochPeriod()
		gracePeriod := s.allocStore.GetGracePeriod()
		allocation.ExpiresAt = now.Add(time.Duration(gracePeriod) * epochPeriod)
	}
	if req.AllocType != "" {
		allocation.AllocType = store.AllocationType(req.AllocType)
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
		NodeID:       a.NodeID,
		BackupNodeID: a.BackupNodeID,
		IsBackup:     a.IsBackup,
		TTL:          a.TTL,
		Epoch:        a.Epoch,
		ExpiresAt:    a.ExpiresAt,
		LastRenewed:  a.LastRenewed,
		AllocType:    string(a.AllocType),
	}
}

// createBackupAllocations assigns backup allocations for a pool to a standby node.
// POST /api/v1/pools/{pool_id}/backup-allocations
func (s *Server) createBackupAllocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	poolID := vars["pool_id"]

	// Validate pool ID from URL
	if err := validation.ValidatePoolID(poolID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req BackupAllocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		respondError(w, http.StatusBadRequest, "invalid request body: malformed JSON")
		return
	}

	// Validate backup node ID
	if err := validation.ValidateNodeID(req.BackupNodeID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Get the pool to check backup ratio
	pool, err := s.poolStore.GetPool(ctx, poolID)
	if err != nil {
		respondError(w, http.StatusNotFound, "pool not found")
		return
	}

	// Get all allocations for this pool
	allocations, err := s.allocStore.ListAllocationsByPool(ctx, poolID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Calculate how many allocations this backup node should receive based on backup ratio
	// For now, we assign backup status to all allocations that don't already have a backup node
	assignedCount := 0
	for _, alloc := range allocations {
		// Skip allocations that already have a backup node or are themselves backups
		if alloc.BackupNodeID != "" || alloc.IsBackup {
			continue
		}

		// Skip allocations owned by the backup node itself (can't be your own backup)
		if alloc.NodeID == req.BackupNodeID {
			continue
		}

		// Assign this allocation to the backup node
		if err := s.allocStore.AssignBackupNode(ctx, poolID, alloc.SubscriberID, req.BackupNodeID); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		assignedCount++

		// Limit by backup ratio if set
		if pool.BackupRatio > 0 {
			maxBackups := int(float64(len(allocations)) * pool.BackupRatio)
			if assignedCount >= maxBackups {
				break
			}
		}
	}

	respondJSON(w, http.StatusCreated, BackupAllocationResponse{
		PoolID:           poolID,
		BackupNodeID:     req.BackupNodeID,
		AllocationsCount: assignedCount,
	})
}

// listBackupAllocations returns all backup allocations for a node.
// GET /api/v1/nodes/{node_id}/backup-allocations
func (s *Server) listBackupAllocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	nodeID := vars["node_id"]

	// Validate node ID from URL
	if err := validation.ValidateNodeID(nodeID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	allocations, err := s.allocStore.ListBackupAllocationsByNode(ctx, nodeID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]AllocationResponse, 0, len(allocations))
	for _, a := range allocations {
		response = append(response, allocationToResponse(a))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"allocations":    response,
		"count":          len(response),
		"backup_node_id": nodeID,
	})
}

// listNodeAllocations returns all primary allocations for a node.
// GET /api/v1/nodes/{node_id}/allocations
func (s *Server) listNodeAllocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	nodeID := vars["node_id"]

	// Validate node ID from URL
	if err := validation.ValidateNodeID(nodeID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	allocations, err := s.allocStore.ListAllocationsByNode(ctx, nodeID)
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
		"node_id":     nodeID,
	})
}

// renewAllocation renews an allocation's TTL.
// POST /api/v1/allocations/{subscriber_id}/renew
func (s *Server) renewAllocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	subscriberID := vars["subscriber_id"]

	// Validate subscriber ID from URL
	if err := validation.ValidateSubscriberID(subscriberID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req RenewAllocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		respondError(w, http.StatusBadRequest, "invalid request body: malformed JSON")
		return
	}

	// Find the allocation first to get pool ID
	existing, err := s.allocStore.GetAllocationBySubscriber(ctx, subscriberID)
	if err != nil {
		respondError(w, http.StatusNotFound, "allocation not found")
		return
	}

	// Renew the allocation
	renewed, err := s.allocStore.RenewAllocation(ctx, existing.PoolID, subscriberID, req.TTL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, allocationToResponse(renewed))
}

// listExpiringAllocations returns allocations that expire before a given time.
// GET /api/v1/allocations/expiring?within=3600
func (s *Server) listExpiringAllocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse "within" parameter (seconds from now)
	withinStr := r.URL.Query().Get("within")
	if withinStr == "" {
		withinStr = "3600" // Default to 1 hour
	}

	withinSeconds, err := strconv.ParseInt(withinStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid 'within' parameter: must be seconds")
		return
	}

	before := time.Now().Add(time.Duration(withinSeconds) * time.Second)

	allocations, err := s.allocStore.ListExpiringAllocations(ctx, before)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]AllocationResponse, 0, len(allocations))
	for _, a := range allocations {
		response = append(response, allocationToResponse(a))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"allocations":     response,
		"count":           len(response),
		"expiring_before": before,
	})
}
