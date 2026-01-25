package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/store"
)

// TestListNodes_TableDriven uses table-driven tests for node listing
func TestListNodes_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		nodes          []*store.Node
		expectedStatus int
		expectedCount  int
	}{
		{
			name:           "empty nodes",
			nodes:          []*store.Node{},
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
		{
			name: "single active node",
			nodes: []*store.Node{
				{
					ID:         "node1",
					BestBefore: time.Now().Add(time.Hour),
					Metadata:   map[string]string{"role": "write"},
				},
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
		},
		{
			name: "multiple nodes mixed status",
			nodes: []*store.Node{
				{
					ID:         "node1",
					BestBefore: time.Now().Add(time.Hour),
					Metadata:   map[string]string{"role": "write"},
				},
				{
					ID:         "node2",
					BestBefore: time.Now().Add(-time.Hour), // Expired
					Metadata:   map[string]string{"role": "write"},
				},
				{
					ID:         "node3",
					BestBefore: time.Now().Add(2 * time.Hour),
					Metadata:   map[string]string{"role": "read"},
				},
			},
			expectedStatus: http.StatusOK,
			expectedCount:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, nodeStore, _ := setupTestServer()
			router := setupTestRouter(server)

			nodeStore.nodes = tt.nodes

			req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("listNodes returned status %d, want %d", w.Code, tt.expectedStatus)
			}

			var response map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}

			count, ok := response["count"].(float64)
			if !ok || int(count) != tt.expectedCount {
				t.Errorf("listNodes count = %v, want %d", response["count"], tt.expectedCount)
			}
		})
	}
}

// TestNodeToResponse_TableDriven uses table-driven tests for node response conversion
func TestNodeToResponse_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		node           *store.Node
		expectedStatus string
	}{
		{
			name: "active node",
			node: &store.Node{
				ID:         "node1",
				BestBefore: time.Now().Add(time.Hour),
				Metadata:   map[string]string{"role": "write"},
			},
			expectedStatus: "active",
		},
		{
			name: "expired node",
			node: &store.Node{
				ID:         "node2",
				BestBefore: time.Now().Add(-time.Hour),
				Metadata:   map[string]string{"role": "write"},
			},
			expectedStatus: "expired",
		},
		{
			name: "just expired node",
			node: &store.Node{
				ID:         "node3",
				BestBefore: time.Now().Add(-time.Millisecond),
				Metadata:   map[string]string{"role": "read"},
			},
			expectedStatus: "expired",
		},
		{
			name: "about to expire node",
			node: &store.Node{
				ID:         "node4",
				BestBefore: time.Now().Add(time.Millisecond),
				Metadata:   map[string]string{"role": "core"},
			},
			expectedStatus: "active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := nodeToResponse(tt.node)

			if response.ID != tt.node.ID {
				t.Errorf("nodeToResponse ID = %s, want %s", response.ID, tt.node.ID)
			}
			if response.Status != tt.expectedStatus {
				t.Errorf("nodeToResponse Status = %s, want %s", response.Status, tt.expectedStatus)
			}
			if !response.BestBefore.Equal(tt.node.BestBefore) {
				t.Errorf("nodeToResponse BestBefore = %v, want %v", response.BestBefore, tt.node.BestBefore)
			}
		})
	}
}

// TestNodeToResponse_WithMetadata tests node response with various metadata
func TestNodeToResponse_WithMetadata(t *testing.T) {
	node := &store.Node{
		ID:         "node1",
		BestBefore: time.Now().Add(time.Hour),
		Metadata: map[string]string{
			"role":   "write",
			"region": "us-east-1",
			"site":   "datacenter-1",
		},
	}

	response := nodeToResponse(node)

	if len(response.Metadata) != 3 {
		t.Errorf("nodeToResponse Metadata length = %d, want 3", len(response.Metadata))
	}

	expectedMeta := map[string]string{
		"role":   "write",
		"region": "us-east-1",
		"site":   "datacenter-1",
	}
	for k, v := range expectedMeta {
		if response.Metadata[k] != v {
			t.Errorf("nodeToResponse Metadata[%s] = %s, want %s", k, response.Metadata[k], v)
		}
	}
}

// TestNodeToResponse_NilMetadata tests node response with nil metadata
func TestNodeToResponse_NilMetadata(t *testing.T) {
	node := &store.Node{
		ID:         "node1",
		BestBefore: time.Now().Add(time.Hour),
		Metadata:   nil,
	}

	response := nodeToResponse(node)

	if response.Metadata != nil {
		t.Errorf("nodeToResponse Metadata should be nil, got %v", response.Metadata)
	}
}

// TestListNodes_StoreError tests node listing when store returns error
func TestListNodes_StoreError(t *testing.T) {
	server, _, nodeStore, _ := setupTestServer()
	router := setupTestRouter(server)

	// Set error on node store
	nodeStore.err = store.ErrPoolNotFound // Reusing error for testing

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("listNodes with error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if _, ok := response["error"]; !ok {
		t.Error("response should contain 'error' field")
	}
}

// TestListNodes_ResponseFormat tests the response format of node listing
func TestListNodes_ResponseFormat(t *testing.T) {
	server, _, nodeStore, _ := setupTestServer()
	router := setupTestRouter(server)

	nodeStore.nodes = []*store.Node{
		{
			ID:         "node1",
			BestBefore: time.Now().Add(time.Hour),
			Metadata:   map[string]string{"role": "write"},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listNodes returned status %d, want %d", w.Code, http.StatusOK)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("listNodes Content-Type = %s, want application/json", contentType)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check required fields
	if _, ok := response["nodes"]; !ok {
		t.Error("response should contain 'nodes' field")
	}
	if _, ok := response["count"]; !ok {
		t.Error("response should contain 'count' field")
	}

	// Check nodes array structure
	nodes, ok := response["nodes"].([]interface{})
	if !ok {
		t.Fatal("nodes should be an array")
	}
	if len(nodes) != 1 {
		t.Errorf("nodes length = %d, want 1", len(nodes))
	}

	// Check node structure
	node, ok := nodes[0].(map[string]interface{})
	if !ok {
		t.Fatal("node should be an object")
	}

	expectedFields := []string{"id", "best_before", "status"}
	for _, field := range expectedFields {
		if _, ok := node[field]; !ok {
			t.Errorf("node should contain '%s' field", field)
		}
	}
}

// TestListNodes_StatusCalculation tests that node status is correctly calculated
func TestListNodes_StatusCalculation(t *testing.T) {
	server, _, nodeStore, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add nodes with different expiration times
	nodeStore.nodes = []*store.Node{
		{
			ID:         "active-node",
			BestBefore: time.Now().Add(time.Hour),
			Metadata:   nil,
		},
		{
			ID:         "expired-node",
			BestBefore: time.Now().Add(-time.Hour),
			Metadata:   nil,
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	nodes := response["nodes"].([]interface{})

	activeFound := false
	expiredFound := false

	for _, n := range nodes {
		node := n.(map[string]interface{})
		id := node["id"].(string)
		status := node["status"].(string)

		if id == "active-node" && status == "active" {
			activeFound = true
		}
		if id == "expired-node" && status == "expired" {
			expiredFound = true
		}
	}

	if !activeFound {
		t.Error("active-node should have status 'active'")
	}
	if !expiredFound {
		t.Error("expired-node should have status 'expired'")
	}
}
