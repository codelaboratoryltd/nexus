package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	ds "github.com/ipfs/go-datastore"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/resource"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// Mock implementations for testing

type mockPoolStore struct {
	pools map[string]*store.Pool
	err   error
}

func newMockPoolStore() *mockPoolStore {
	return &mockPoolStore{
		pools: make(map[string]*store.Pool),
	}
}

func (m *mockPoolStore) SavePool(ctx context.Context, pool *store.Pool) error {
	if m.err != nil {
		return m.err
	}
	m.pools[pool.ID] = pool
	return nil
}

func (m *mockPoolStore) GetPool(ctx context.Context, poolID string) (*store.Pool, error) {
	if m.err != nil {
		return nil, m.err
	}
	pool, ok := m.pools[poolID]
	if !ok {
		return nil, store.ErrPoolNotFound
	}
	return pool, nil
}

func (m *mockPoolStore) DeletePool(ctx context.Context, poolID string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.pools, poolID)
	return nil
}

func (m *mockPoolStore) ListPools(ctx context.Context) ([]*store.Pool, error) {
	if m.err != nil {
		return nil, m.err
	}
	pools := make([]*store.Pool, 0, len(m.pools))
	for _, p := range m.pools {
		pools = append(pools, p)
	}
	return pools, nil
}

func (m *mockPoolStore) UnmarshalKey(key ds.Key, value []byte) (*store.Pool, error) {
	return nil, nil
}

type mockNodeStore struct {
	nodes []*store.Node
	err   error
}

func newMockNodeStore() *mockNodeStore {
	return &mockNodeStore{
		nodes: make([]*store.Node, 0),
	}
}

func (m *mockNodeStore) ListNodes(ctx context.Context) ([]*store.Node, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.nodes, nil
}

type mockAllocationStore struct {
	allocations map[string]*store.Allocation
	err         error
}

func newMockAllocationStore() *mockAllocationStore {
	return &mockAllocationStore{
		allocations: make(map[string]*store.Allocation),
	}
}

func (m *mockAllocationStore) SaveAllocation(ctx context.Context, a *store.Allocation) error {
	if m.err != nil {
		return m.err
	}
	m.allocations[a.SubscriberID] = a
	return nil
}

func (m *mockAllocationStore) RemoveAllocation(ctx context.Context, poolID, subscriberID string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.allocations, subscriberID)
	return nil
}

func (m *mockAllocationStore) GetAllocation(ctx context.Context, poolID, subscriberID string) (*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	alloc, ok := m.allocations[subscriberID]
	if !ok {
		return nil, store.ErrNoAllocationFound
	}
	return alloc, nil
}

func (m *mockAllocationStore) LoadAllocatorState(ctx context.Context, poolID string, allocator resource.Allocator) error {
	return nil
}

func (m *mockAllocationStore) GetAllocationBySubscriber(ctx context.Context, subscriberID string) (*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	alloc, ok := m.allocations[subscriberID]
	if !ok {
		return nil, store.ErrNoAllocationFound
	}
	return alloc, nil
}

func (m *mockAllocationStore) UnmarshalAllocationKey(key ds.Key) (*store.AllocationKey, error) {
	return nil, nil
}

func (m *mockAllocationStore) UnmarshalSubscriberKey(key ds.Key, subscriberID string) (*store.AllocationKey, error) {
	return nil, nil
}

func (m *mockAllocationStore) ListAllocationsByPool(ctx context.Context, poolID string) ([]*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	allocs := make([]*store.Allocation, 0)
	for _, a := range m.allocations {
		if a.PoolID == poolID {
			allocs = append(allocs, a)
		}
	}
	return allocs, nil
}

func (m *mockAllocationStore) CountAllocationsByPool(ctx context.Context, poolID string) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	count := 0
	for _, a := range m.allocations {
		if a.PoolID == poolID {
			count++
		}
	}
	return count, nil
}

// Test setup helpers

func setupTestServer() (*Server, *mockPoolStore, *mockNodeStore, *mockAllocationStore) {
	ring := hashring.NewVirtualNodesHashRing()
	poolStore := newMockPoolStore()
	nodeStore := newMockNodeStore()
	allocStore := newMockAllocationStore()

	server := NewServer(ring, poolStore, nodeStore, allocStore)
	return server, poolStore, nodeStore, allocStore
}

func setupTestRouter(server *Server) *mux.Router {
	r := mux.NewRouter()
	server.RegisterRoutes(r)
	return r
}

// Tests

func TestNewServer(t *testing.T) {
	ring := hashring.NewVirtualNodesHashRing()
	poolStore := newMockPoolStore()
	nodeStore := newMockNodeStore()
	allocStore := newMockAllocationStore()

	server := NewServer(ring, poolStore, nodeStore, allocStore)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestHealthHandler(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health handler returned status %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ok" {
		t.Errorf("health handler returned body %q, want %q", w.Body.String(), "ok")
	}
}

func TestReadyHandler(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready handler returned status %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ready" {
		t.Errorf("ready handler returned body %q, want %q", w.Body.String(), "ready")
	}
}

func TestListPools_Empty(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

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

	pools, ok := response["pools"].([]interface{})
	if !ok {
		t.Fatal("Response does not contain pools array")
	}
	if len(pools) != 0 {
		t.Errorf("Expected empty pools, got %d", len(pools))
	}
}

func TestListPools_WithPools(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

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
	if !ok || count != 1 {
		t.Errorf("Expected count 1, got %v", response["count"])
	}
}

func TestCreatePool_Success(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createPool returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestCreatePool_MissingID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"cidr": "192.168.0.0/24"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreatePool_MissingCIDR(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"id": "pool1"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreatePool_InvalidCIDR(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"id": "pool1", "cidr": "not-a-cidr"}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreatePool_InvalidJSON(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{invalid json}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetPool_NotFound(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/pools/nonexistent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("getPool returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetPool_Success(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

	req := httptest.NewRequest("GET", "/api/v1/pools/pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("getPool returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response PoolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.ID != "pool1" {
		t.Errorf("Expected pool ID 'pool1', got %q", response.ID)
	}
}

func TestDeletePool_Success(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

	req := httptest.NewRequest("DELETE", "/api/v1/pools/pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("deletePool returned status %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestListNodes_Empty(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listNodes returned status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestListNodes_WithNodes(t *testing.T) {
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

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 1 {
		t.Errorf("Expected count 1, got %v", response["count"])
	}
}

func TestListAllocations_MissingPoolID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("listAllocations returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestListAllocations_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	req := httptest.NewRequest("GET", "/api/v1/allocations?pool_id=pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listAllocations returned status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCreateAllocation_MissingPoolID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"subscriber_id": "sub1"}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createAllocation returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAllocation_MissingSubscriberID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"pool_id": "pool1"}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createAllocation returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAllocation_InvalidJSON(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{invalid}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createAllocation returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetAllocation_NotFound(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/allocations/nonexistent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("getAllocation returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetAllocation_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	req := httptest.NewRequest("GET", "/api/v1/allocations/sub1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("getAllocation returned status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDeleteAllocation_NotFound(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("DELETE", "/api/v1/allocations/nonexistent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("deleteAllocation returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteAllocation_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/allocations/sub1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("deleteAllocation returned status %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestNodeToResponse_Active(t *testing.T) {
	node := &store.Node{
		ID:         "node1",
		BestBefore: time.Now().Add(time.Hour), // Future time
		Metadata:   map[string]string{"role": "write"},
	}

	response := nodeToResponse(node)

	if response.ID != "node1" {
		t.Errorf("nodeToResponse ID = %s, want node1", response.ID)
	}
	if response.Status != "active" {
		t.Errorf("nodeToResponse Status = %s, want active", response.Status)
	}
}

func TestNodeToResponse_Expired(t *testing.T) {
	node := &store.Node{
		ID:         "node1",
		BestBefore: time.Now().Add(-time.Hour), // Past time
		Metadata:   map[string]string{"role": "write"},
	}

	response := nodeToResponse(node)

	if response.Status != "expired" {
		t.Errorf("nodeToResponse Status = %s, want expired", response.Status)
	}
}

func TestPoolToResponse(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &store.Pool{
		ID:             "pool1",
		CIDR:           *cidr,
		Prefix:         28,
		Exclusions:     []string{"192.168.0.0/30"},
		Metadata:       map[string]string{"env": "prod"},
		ShardingFactor: 4,
	}

	response := poolToResponse(pool)

	if response.ID != "pool1" {
		t.Errorf("poolToResponse ID = %s, want pool1", response.ID)
	}
	if response.CIDR != "192.168.0.0/24" {
		t.Errorf("poolToResponse CIDR = %s, want 192.168.0.0/24", response.CIDR)
	}
	if response.Prefix != 28 {
		t.Errorf("poolToResponse Prefix = %d, want 28", response.Prefix)
	}
	if response.ShardingFactor != 4 {
		t.Errorf("poolToResponse ShardingFactor = %d, want 4", response.ShardingFactor)
	}
}

func TestAllocationToResponse(t *testing.T) {
	timestamp := time.Now()
	alloc := &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    timestamp,
	}

	response := allocationToResponse(alloc)

	if response.PoolID != "pool1" {
		t.Errorf("allocationToResponse PoolID = %s, want pool1", response.PoolID)
	}
	if response.SubscriberID != "sub1" {
		t.Errorf("allocationToResponse SubscriberID = %s, want sub1", response.SubscriberID)
	}
	if response.IP != "192.168.0.1" {
		t.Errorf("allocationToResponse IP = %s, want 192.168.0.1", response.IP)
	}
}

func TestRespondJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	respondJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("respondJSON status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("respondJSON Content-Type = %s, want application/json", contentType)
	}
}

func TestRespondError(t *testing.T) {
	w := httptest.NewRecorder()

	respondError(w, http.StatusBadRequest, "test error")

	if w.Code != http.StatusBadRequest {
		t.Errorf("respondError status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["error"] != "test error" {
		t.Errorf("respondError message = %s, want 'test error'", response["error"])
	}
}

func TestRespondJSON_NilData(t *testing.T) {
	w := httptest.NewRecorder()

	respondJSON(w, http.StatusNoContent, nil)

	if w.Code != http.StatusNoContent {
		t.Errorf("respondJSON status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if w.Body.Len() != 0 {
		t.Errorf("respondJSON body length = %d, want 0", w.Body.Len())
	}
}

func TestListPools_Error(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Set error on pool store
	poolStore.err = store.ErrPoolNotFound

	req := httptest.NewRequest("GET", "/api/v1/pools", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("listPools with error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreatePool_ValidationError(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Create pool with invalid prefix (smaller than CIDR mask)
	body := `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 16}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool with invalid prefix returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreatePool_StoreError(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Set error on pool store
	poolStore.err = store.ErrPoolNotFound

	body := `{"id": "pool1", "cidr": "192.168.0.0/24", "prefix": 28}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("createPool with store error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDeletePool_Error(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Set error on pool store
	poolStore.err = store.ErrPoolNotFound

	req := httptest.NewRequest("DELETE", "/api/v1/pools/pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("deletePool with error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestListNodes_Error(t *testing.T) {
	server, _, nodeStore, _ := setupTestServer()
	router := setupTestRouter(server)

	// Set error on node store
	nodeStore.err = store.ErrPoolNotFound

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("listNodes with error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestListAllocations_Error(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Set error on allocation store
	allocStore.err = store.ErrNoAllocationFound

	req := httptest.NewRequest("GET", "/api/v1/allocations?pool_id=pool1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("listAllocations with error returned status %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRegisterRoutes(t *testing.T) {
	server, _, _, _ := setupTestServer()
	r := mux.NewRouter()

	// Should not panic
	server.RegisterRoutes(r)

	// Verify routes were registered by checking a few endpoints
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health endpoint not registered properly, got status %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/ready", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready endpoint not registered properly, got status %d", w.Code)
	}
}

func TestCreatePool_WithAllFields(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{
		"id": "pool1",
		"cidr": "192.168.0.0/24",
		"prefix": 28,
		"exclusions": ["192.168.0.0/30"],
		"metadata": {"env": "test"},
		"sharding_factor": 4
	}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createPool returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response PoolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.ShardingFactor != 4 {
		t.Errorf("createPool ShardingFactor = %d, want 4", response.ShardingFactor)
	}
	if len(response.Exclusions) != 1 {
		t.Errorf("createPool Exclusions length = %d, want 1", len(response.Exclusions))
	}
}
