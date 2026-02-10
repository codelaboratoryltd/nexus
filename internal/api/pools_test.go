package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codelaboratoryltd/nexus/internal/store"
)

// TestCreatePool_TableDriven uses table-driven tests for pool creation
func TestCreatePool_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "valid pool basic",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "valid pool with all fields",
			body:           `{"id": "pool2", "cidr": "10.0.0.0/16", "prefix": 24, "exclusions": ["10.0.0.0/24"], "metadata": {"env": "test"}, "sharding_factor": 4, "backup_ratio": 0.1}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "empty id",
			body:           `{"id": "", "cidr": "192.168.0.0/24", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
		{
			name:           "missing id",
			body:           `{"cidr": "192.168.0.0/24", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
		{
			name:           "empty cidr",
			body:           `{"id": "pool1", "cidr": "", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "CIDR",
		},
		{
			name:           "invalid cidr format",
			body:           `{"id": "pool1", "cidr": "not-a-cidr", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "CIDR",
		},
		{
			name:           "cidr host address not network",
			body:           `{"id": "pool1", "cidr": "192.168.1.1/24", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "network address",
		},
		{
			name:           "prefix smaller than cidr mask",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 16}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "prefix",
		},
		{
			name:           "invalid sharding factor negative",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "sharding_factor": -1}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "sharding",
		},
		{
			name:           "invalid sharding factor too large",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "sharding_factor": 300}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "sharding",
		},
		{
			name:           "invalid backup ratio negative",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "backup_ratio": -0.1}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "backup_ratio",
		},
		{
			name:           "invalid backup ratio over 1",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "backup_ratio": 1.5}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "backup_ratio",
		},
		{
			name:           "invalid exclusion format",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "exclusions": ["not-a-cidr"]}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "exclusion",
		},
		{
			name:           "invalid metadata key",
			body:           `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28, "metadata": {"1invalid": "value"}}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "metadata",
		},
		{
			name:           "malformed json",
			body:           `{"id": "pool1", "cidr":}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "malformed JSON",
		},
		{
			name:           "id with special characters",
			body:           `{"id": "pool'; DROP TABLE--", "cidr": "192.168.0.0/24", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
		{
			name:           "id starting with hyphen",
			body:           `{"id": "-pool1", "cidr": "192.168.0.0/24", "prefix": 28}`,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "pool_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _, _ := setupTestServer()
			router := setupTestRouter(server)

			req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("createPool returned status %d, want %d: %s", w.Code, tt.expectedStatus, w.Body.String())
			}

			if tt.expectedError != "" && w.Code != http.StatusCreated {
				if !strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(tt.expectedError)) {
					t.Errorf("error message should contain %q, got: %s", tt.expectedError, w.Body.String())
				}
			}
		})
	}
}

// TestGetPool_InvalidID tests get pool with invalid ID validation
func TestGetPool_InvalidID(t *testing.T) {
	tests := []struct {
		name           string
		poolID         string
		expectedStatus int
	}{
		{"empty id", "", http.StatusNotFound}, // Router won't match empty
		{"id with spaces", "pool%20name", http.StatusBadRequest},
		{"id starting with hyphen", "-pool", http.StatusBadRequest},
		{"id ending with hyphen", "pool-", http.StatusBadRequest},
		{"special characters", "pool@123", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _, _ := setupTestServer()
			router := setupTestRouter(server)

			req := httptest.NewRequest("GET", "/api/v1/pools/"+tt.poolID, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("getPool(%s) returned status %d, want %d", tt.poolID, w.Code, tt.expectedStatus)
			}
		})
	}
}

// TestDeletePool_InvalidID tests delete pool with invalid ID validation
func TestDeletePool_InvalidID(t *testing.T) {
	tests := []struct {
		name           string
		poolID         string
		expectedStatus int
	}{
		{"id starting with hyphen", "-pool", http.StatusBadRequest},
		{"id ending with hyphen", "pool-", http.StatusBadRequest},
		{"special characters", "pool@123", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _, _ := setupTestServer()
			router := setupTestRouter(server)

			req := httptest.NewRequest("DELETE", "/api/v1/pools/"+tt.poolID, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("deletePool(%s) returned status %d, want %d", tt.poolID, w.Code, tt.expectedStatus)
			}
		})
	}
}

// TestListPools_WithMultiplePools tests listing pools with multiple entries
func TestListPools_WithMultiplePools(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add multiple pools
	_, cidr1, _ := net.ParseCIDR("192.168.0.0/24")
	_, cidr2, _ := net.ParseCIDR("10.0.0.0/16")
	_, cidr3, _ := net.ParseCIDR("172.16.0.0/12")

	poolStore.pools["pool1"] = &store.Pool{ID: "pool1", CIDR: *cidr1, Prefix: 28}
	poolStore.pools["pool2"] = &store.Pool{ID: "pool2", CIDR: *cidr2, Prefix: 24}
	poolStore.pools["pool3"] = &store.Pool{ID: "pool3", CIDR: *cidr3, Prefix: 20}

	req := httptest.NewRequest("GET", "/api/v1/pools", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listPools returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 3 {
		t.Errorf("Expected count 3, got %v", response["count"])
	}

	pools, ok := response["pools"].([]interface{})
	if !ok || len(pools) != 3 {
		t.Errorf("Expected 3 pools, got %d", len(pools))
	}
}

// TestPoolToResponse_AllFields tests pool response conversion with all fields
func TestPoolToResponse_AllFields(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &store.Pool{
		ID:             "test-pool",
		CIDR:           *cidr,
		Prefix:         28,
		Exclusions:     []string{"192.168.0.0/30", "192.168.0.4/30"},
		Metadata:       map[string]string{"env": "prod", "region": "us-east"},
		ShardingFactor: 8,
		BackupRatio:    0.15,
	}

	response := poolToResponse(pool)

	if response.ID != pool.ID {
		t.Errorf("poolToResponse ID = %s, want %s", response.ID, pool.ID)
	}
	if response.CIDR != "192.168.0.0/24" {
		t.Errorf("poolToResponse CIDR = %s, want 192.168.0.0/24", response.CIDR)
	}
	if response.Prefix != 28 {
		t.Errorf("poolToResponse Prefix = %d, want 28", response.Prefix)
	}
	if len(response.Exclusions) != 2 {
		t.Errorf("poolToResponse Exclusions length = %d, want 2", len(response.Exclusions))
	}
	if len(response.Metadata) != 2 {
		t.Errorf("poolToResponse Metadata length = %d, want 2", len(response.Metadata))
	}
	if response.ShardingFactor != 8 {
		t.Errorf("poolToResponse ShardingFactor = %d, want 8", response.ShardingFactor)
	}
	if response.BackupRatio != 0.15 {
		t.Errorf("poolToResponse BackupRatio = %f, want 0.15", response.BackupRatio)
	}
}

// TestPoolToResponse_EmptyOptionalFields tests pool response with nil/empty optional fields
func TestPoolToResponse_EmptyOptionalFields(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &store.Pool{
		ID:     "test-pool",
		CIDR:   *cidr,
		Prefix: 28,
		// No exclusions, metadata, etc.
	}

	response := poolToResponse(pool)

	if len(response.Exclusions) != 0 {
		t.Errorf("poolToResponse Exclusions should be empty, got %v", response.Exclusions)
	}
	if len(response.Metadata) != 0 {
		t.Errorf("poolToResponse Metadata should be empty, got %v", response.Metadata)
	}
	if response.ShardingFactor != 0 {
		t.Errorf("poolToResponse ShardingFactor = %d, want 0", response.ShardingFactor)
	}
	if response.BackupRatio != 0 {
		t.Errorf("poolToResponse BackupRatio = %f, want 0", response.BackupRatio)
	}
}

// TestCreatePool_IPv6 tests pool creation with IPv6 CIDR
func TestCreatePool_IPv6(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"id": "ipv6pool", "cidr": "2001:db8::/32", "prefix": 64}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createPool (IPv6) returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response PoolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.CIDR != "2001:db8::/32" {
		t.Errorf("createPool (IPv6) CIDR = %s, want 2001:db8::/32", response.CIDR)
	}
}
