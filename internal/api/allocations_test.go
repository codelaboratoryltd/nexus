package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/store"
)

// TestCreateAllocation_TableDriven uses table-driven tests for allocation creation
func TestCreateAllocation_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		setupPool      bool
		body           string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "valid allocation basic",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub1"}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "valid allocation with node_id",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub2", "node_id": "bng-node-1"}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "valid allocation with specific IP",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub3", "ip": "192.168.0.10"}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "valid allocation with TTL",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub4", "ttl": 3600, "alloc_type": "session"}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "empty pool_id",
			setupPool:      true,
			body:           `{"pool_id": "", "subscriber_id": "sub1"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
		{
			name:           "missing pool_id",
			setupPool:      true,
			body:           `{"subscriber_id": "sub1"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
		{
			name:           "empty subscriber_id",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": ""}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "subscriber_id",
		},
		{
			name:           "missing subscriber_id",
			setupPool:      true,
			body:           `{"pool_id": "pool1"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "subscriber_id",
		},
		{
			name:           "invalid node_id format",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub1", "node_id": "--invalid"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "node_id",
		},
		{
			name:           "invalid ip format",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub1", "ip": "not-an-ip"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "IP",
		},
		{
			name:           "malformed json",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id":}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "malformed JSON",
		},
		{
			name:           "subscriber_id with special characters",
			setupPool:      true,
			body:           `{"pool_id": "pool1", "subscriber_id": "sub'; DROP TABLE--"}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "subscriber_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, poolStore, _, _ := setupTestServer()
			router := setupTestRouter(server)

			if tt.setupPool {
				_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
				poolStore.pools["pool1"] = &store.Pool{
					ID:     "pool1",
					CIDR:   *cidr,
					Prefix: 28,
				}
				server.ring.AddPool("pool1", cidr)
			}

			req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("createAllocation returned status %d, want %d: %s", w.Code, tt.expectedStatus, w.Body.String())
			}

			if tt.expectedError != "" && w.Code != http.StatusCreated {
				if !strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(tt.expectedError)) {
					t.Errorf("error message should contain %q, got: %s", tt.expectedError, w.Body.String())
				}
			}
		})
	}
}

// TestCreateAllocation_DuplicateSubscriber tests conflict when subscriber already has allocation
func TestCreateAllocation_DuplicateSubscriber(t *testing.T) {
	server, poolStore, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Setup pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}
	server.ring.AddPool("pool1", cidr)

	// Add existing allocation
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	// Try to create another allocation for same subscriber
	body := `{"pool_id": "pool1", "subscriber_id": "sub1"}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("createAllocation (duplicate) returned status %d, want %d", w.Code, http.StatusConflict)
	}
}

// TestGetAllocation_InvalidSubscriberID tests get allocation with invalid subscriber ID
func TestGetAllocation_InvalidSubscriberID(t *testing.T) {
	tests := []struct {
		name           string
		subscriberID   string
		expectedStatus int
	}{
		{"subscriber with spaces", "sub%20name", http.StatusBadRequest},
		{"starting with hyphen", "-sub", http.StatusBadRequest},
		{"ending with hyphen", "sub-", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _, _ := setupTestServer()
			router := setupTestRouter(server)

			req := httptest.NewRequest("GET", "/api/v1/allocations/"+tt.subscriberID, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("getAllocation(%s) returned status %d, want %d: %s", tt.subscriberID, w.Code, tt.expectedStatus, w.Body.String())
			}
		})
	}
}

// TestDeleteAllocation_WithPoolID tests delete allocation with pool_id query parameter
func TestDeleteAllocation_WithPoolID(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add allocation
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/allocations/sub1?pool_id=pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("deleteAllocation returned status %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify deletion
	if _, exists := allocStore.allocations["sub1"]; exists {
		t.Error("allocation should be deleted")
	}
}

// TestDeleteAllocation_InvalidPoolID tests delete allocation with invalid pool_id
func TestDeleteAllocation_InvalidPoolID(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add allocation
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/allocations/sub1?pool_id=--invalid", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("deleteAllocation with invalid pool_id returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListAllocations_InvalidPoolID tests list allocations with invalid pool_id
func TestListAllocations_InvalidPoolID(t *testing.T) {
	tests := []struct {
		name           string
		poolID         string
		expectedStatus int
	}{
		{"empty pool_id", "", http.StatusBadRequest},
		{"invalid format", "--invalid", http.StatusBadRequest},
		{"special characters", "pool@123", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _, _ := setupTestServer()
			router := setupTestRouter(server)

			url := "/api/v1/allocations"
			if tt.poolID != "" {
				url += "?pool_id=" + tt.poolID
			}

			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("listAllocations(%s) returned status %d, want %d", tt.poolID, w.Code, tt.expectedStatus)
			}
		})
	}
}

// TestListAllocations_WithMultipleAllocations tests listing allocations with multiple entries
func TestListAllocations_WithMultipleAllocations(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add multiple allocations
	for i := 1; i <= 5; i++ {
		allocStore.allocations["sub"+string(rune('0'+i))] = &store.Allocation{
			PoolID:       "pool1",
			SubscriberID: "sub" + string(rune('0'+i)),
			IP:           net.ParseIP("192.168.0." + string(rune('0'+i))),
			Timestamp:    time.Now(),
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/allocations?pool_id=pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listAllocations returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 5 {
		t.Errorf("Expected count 5, got %v", response["count"])
	}
}

// TestAllocationToResponse_AllFields tests allocation response conversion
func TestAllocationToResponse_AllFields(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	lastRenewed := now.Add(-10 * time.Minute)

	alloc := &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    now,
		NodeID:       "bng-node-1",
		BackupNodeID: "bng-node-2",
		IsBackup:     false,
		TTL:          3600,
		Epoch:        5,
		ExpiresAt:    expiresAt,
		LastRenewed:  lastRenewed,
		AllocType:    store.AllocationTypeSession,
	}

	response := allocationToResponse(alloc)

	if response.PoolID != alloc.PoolID {
		t.Errorf("allocationToResponse PoolID = %s, want %s", response.PoolID, alloc.PoolID)
	}
	if response.SubscriberID != alloc.SubscriberID {
		t.Errorf("allocationToResponse SubscriberID = %s, want %s", response.SubscriberID, alloc.SubscriberID)
	}
	if response.IP != "192.168.0.1" {
		t.Errorf("allocationToResponse IP = %s, want 192.168.0.1", response.IP)
	}
	if response.NodeID != "bng-node-1" {
		t.Errorf("allocationToResponse NodeID = %s, want bng-node-1", response.NodeID)
	}
	if response.BackupNodeID != "bng-node-2" {
		t.Errorf("allocationToResponse BackupNodeID = %s, want bng-node-2", response.BackupNodeID)
	}
	if response.IsBackup != false {
		t.Errorf("allocationToResponse IsBackup = %v, want false", response.IsBackup)
	}
	if response.TTL != 3600 {
		t.Errorf("allocationToResponse TTL = %d, want 3600", response.TTL)
	}
	if response.Epoch != 5 {
		t.Errorf("allocationToResponse Epoch = %d, want 5", response.Epoch)
	}
	if response.AllocType != "session" {
		t.Errorf("allocationToResponse AllocType = %s, want session", response.AllocType)
	}
}

// TestRenewAllocation_Success tests successful allocation renewal
func TestRenewAllocation_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add allocation with TTL
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		TTL:          3600,
		Epoch:        1,
	}

	body := `{"ttl": 7200}`
	req := httptest.NewRequest("POST", "/api/v1/allocations/sub1/renew", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("renewAllocation returned status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestRenewAllocation_NotFound tests renewal of non-existent allocation
func TestRenewAllocation_NotFound(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"ttl": 7200}`
	req := httptest.NewRequest("POST", "/api/v1/allocations/nonexistent/renew", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("renewAllocation (not found) returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestRenewAllocation_InvalidSubscriberID tests renewal with invalid subscriber ID
func TestRenewAllocation_InvalidSubscriberID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"ttl": 7200}`
	req := httptest.NewRequest("POST", "/api/v1/allocations/--invalid/renew", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("renewAllocation (invalid subscriber_id) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestRenewAllocation_MalformedJSON tests renewal with malformed JSON body
func TestRenewAllocation_MalformedJSON(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add allocation
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		TTL:          3600,
	}

	body := `{"ttl":}`
	req := httptest.NewRequest("POST", "/api/v1/allocations/sub1/renew", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("renewAllocation (malformed JSON) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListExpiringAllocations_Success tests listing expiring allocations
func TestListExpiringAllocations_Success(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/allocations/expiring?within=3600", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listExpiringAllocations returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if _, ok := response["allocations"]; !ok {
		t.Error("response should contain 'allocations' field")
	}
	if _, ok := response["count"]; !ok {
		t.Error("response should contain 'count' field")
	}
	if _, ok := response["expiring_before"]; !ok {
		t.Error("response should contain 'expiring_before' field")
	}
}

// TestListExpiringAllocations_DefaultWithin tests listing expiring allocations with default 'within'
func TestListExpiringAllocations_DefaultWithin(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/allocations/expiring", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listExpiringAllocations (default within) returned status %d, want %d", w.Code, http.StatusOK)
	}
}

// TestListExpiringAllocations_InvalidWithin tests listing expiring allocations with invalid 'within'
func TestListExpiringAllocations_InvalidWithin(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/allocations/expiring?within=not-a-number", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("listExpiringAllocations (invalid within) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListNodeAllocations_InvalidNodeID tests list node allocations with invalid node ID
func TestListNodeAllocations_InvalidNodeID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/nodes/--invalid/allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("listNodeAllocations (invalid node_id) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListNodeAllocations_EmptyResult tests list node allocations with no results
func TestListNodeAllocations_EmptyResult(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/nodes/bng-node-1/allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listNodeAllocations (empty) returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("Expected count 0, got %v", response["count"])
	}
}

// TestCreateBackupAllocations_InvalidPoolID tests backup allocations with invalid pool ID
func TestCreateBackupAllocations_InvalidPoolID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"backup_node_id": "bng-node-2"}`
	req := httptest.NewRequest("POST", "/api/v1/pools/--invalid/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createBackupAllocations (invalid pool_id) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreateBackupAllocations_MalformedJSON tests backup allocations with malformed JSON
func TestCreateBackupAllocations_MalformedJSON(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

	body := `{"backup_node_id":}`
	req := httptest.NewRequest("POST", "/api/v1/pools/pool1/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createBackupAllocations (malformed JSON) returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestCreateBackupAllocations_NoBackupRatio tests backup allocations when pool has no backup ratio
func TestCreateBackupAllocations_NoBackupRatio(t *testing.T) {
	server, poolStore, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add pool without backup ratio
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:          "pool1",
		CIDR:        *cidr,
		Prefix:      28,
		BackupRatio: 0, // No backup ratio
	}

	// Add allocations
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}
	allocStore.allocations["sub2"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub2",
		IP:           net.ParseIP("192.168.0.2"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}

	body := `{"backup_node_id": "bng-node-2"}`
	req := httptest.NewRequest("POST", "/api/v1/pools/pool1/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createBackupAllocations (no backup ratio) returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response BackupAllocationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Without backup ratio limit, should assign all allocations
	if response.AllocationsCount != 2 {
		t.Errorf("createBackupAllocations AllocationsCount = %d, want 2", response.AllocationsCount)
	}
}

// TestCreateBackupAllocations_SkipsSelfBackup tests that backup allocations skip allocations owned by backup node
func TestCreateBackupAllocations_SkipsSelfBackup(t *testing.T) {
	server, poolStore, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:          "pool1",
		CIDR:        *cidr,
		Prefix:      28,
		BackupRatio: 1.0,
	}

	// Add allocations - one owned by the backup node
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}
	allocStore.allocations["sub2"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub2",
		IP:           net.ParseIP("192.168.0.2"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-2", // Same as backup node
	}

	body := `{"backup_node_id": "bng-node-2"}`
	req := httptest.NewRequest("POST", "/api/v1/pools/pool1/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createBackupAllocations returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response BackupAllocationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should only assign 1 allocation (skip the one owned by bng-node-2)
	if response.AllocationsCount != 1 {
		t.Errorf("createBackupAllocations AllocationsCount = %d, want 1", response.AllocationsCount)
	}
}
