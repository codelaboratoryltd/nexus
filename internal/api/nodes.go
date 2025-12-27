package api

import (
	"net/http"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/store"
)

// NodeResponse represents a node in API responses.
type NodeResponse struct {
	ID         string            `json:"id"`
	BestBefore time.Time         `json:"best_before"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Status     string            `json:"status"`
}

// listNodes returns all cluster nodes.
func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	nodes, err := s.nodeStore.ListNodes(ctx)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]NodeResponse, 0, len(nodes))
	for _, n := range nodes {
		response = append(response, nodeToResponse(n))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": response,
		"count": len(response),
	})
}

// nodeToResponse converts a store.Node to API response.
func nodeToResponse(n *store.Node) NodeResponse {
	status := "active"
	if time.Now().After(n.BestBefore) {
		status = "expired"
	}

	return NodeResponse{
		ID:         n.ID,
		BestBefore: n.BestBefore,
		Metadata:   n.Metadata,
		Status:     status,
	}
}
