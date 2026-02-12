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
	"github.com/codelaboratoryltd/nexus/internal/state"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// mockReadinessChecker implements ReadinessChecker for testing.
type mockReadinessChecker struct {
	peerDiscoveryReady bool
	connectedPeers     int
	syncStatus         state.CRDTSyncStatus
	syncHealthy        bool
}

func (m *mockReadinessChecker) IsPeerDiscoveryReady() bool           { return m.peerDiscoveryReady }
func (m *mockReadinessChecker) ConnectedPeerCount() int              { return m.connectedPeers }
func (m *mockReadinessChecker) CRDTSyncStatus() state.CRDTSyncStatus { return m.syncStatus }
func (m *mockReadinessChecker) IsCRDTSyncHealthy() bool              { return m.syncHealthy }

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

func (m *mockAllocationStore) ListBackupAllocationsByNode(ctx context.Context, nodeID string) ([]*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	allocs := make([]*store.Allocation, 0)
	for _, a := range m.allocations {
		if a.BackupNodeID == nodeID {
			allocs = append(allocs, a)
		}
	}
	return allocs, nil
}

func (m *mockAllocationStore) ListAllocationsByNode(ctx context.Context, nodeID string) ([]*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	allocs := make([]*store.Allocation, 0)
	for _, a := range m.allocations {
		if a.NodeID == nodeID {
			allocs = append(allocs, a)
		}
	}
	return allocs, nil
}

func (m *mockAllocationStore) AssignBackupNode(ctx context.Context, poolID, subscriberID, backupNodeID string) error {
	if m.err != nil {
		return m.err
	}
	alloc, ok := m.allocations[subscriberID]
	if !ok || alloc.PoolID != poolID {
		return store.ErrNoAllocationFound
	}
	alloc.BackupNodeID = backupNodeID
	return nil
}

func (m *mockAllocationStore) RenewAllocation(ctx context.Context, poolID, subscriberID string, newTTL int64) (*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	alloc, ok := m.allocations[subscriberID]
	if !ok || alloc.PoolID != poolID {
		return nil, store.ErrNoAllocationFound
	}
	if newTTL > 0 {
		alloc.TTL = newTTL
	}
	alloc.Epoch = 1
	return alloc, nil
}

func (m *mockAllocationStore) ListExpiringAllocations(ctx context.Context, before time.Time) ([]*store.Allocation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []*store.Allocation{}, nil
}

func (m *mockAllocationStore) CleanupExpiredAllocations(ctx context.Context) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	return 0, nil
}

func (m *mockAllocationStore) GetCurrentEpoch() uint64 {
	return 1
}

func (m *mockAllocationStore) AdvanceEpoch() uint64 {
	return 1
}

func (m *mockAllocationStore) SetEpochPeriod(d time.Duration) {}

func (m *mockAllocationStore) SetGracePeriod(epochs uint64) {}

func (m *mockAllocationStore) GetEpochPeriod() time.Duration {
	return time.Hour
}

func (m *mockAllocationStore) GetGracePeriod() uint64 {
	return 2
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

func TestReadyHandler_Standalone(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready handler returned status %d, want %d", w.Code, http.StatusOK)
	}

	var resp readyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if !resp.Ready {
		t.Error("ready handler returned ready=false, want true")
	}
	if resp.CRDTSync != nil {
		t.Error("ready handler returned crdt_sync in standalone mode, want nil")
	}
}

func TestReadyHandler_WithChecker_Ready(t *testing.T) {
	server, _, _, _ := setupTestServer()

	checker := &mockReadinessChecker{
		peerDiscoveryReady: true,
		connectedPeers:     2,
		syncHealthy:        true,
		syncStatus: state.CRDTSyncStatus{
			PeersConnected: 2,
			SyncLagMs:      150,
			LastSync:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	server.SetReadinessChecker(checker)

	router := setupTestRouter(server)
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready handler returned status %d, want %d", w.Code, http.StatusOK)
	}

	var resp readyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if !resp.Ready {
		t.Error("ready handler returned ready=false, want true")
	}
	if resp.CRDTSync == nil {
		t.Fatal("ready handler returned nil crdt_sync, want non-nil")
	}
	if resp.CRDTSync.PeersConnected != 2 {
		t.Errorf("peers_connected = %d, want 2", resp.CRDTSync.PeersConnected)
	}
	if resp.CRDTSync.SyncLagMs != 150 {
		t.Errorf("sync_lag_ms = %d, want 150", resp.CRDTSync.SyncLagMs)
	}
	if resp.CRDTSync.LastSync != "2024-01-01T00:00:00Z" {
		t.Errorf("last_sync = %s, want 2024-01-01T00:00:00Z", resp.CRDTSync.LastSync)
	}
}

func TestReadyHandler_NotReady_Discovery(t *testing.T) {
	server, _, _, _ := setupTestServer()

	checker := &mockReadinessChecker{
		peerDiscoveryReady: false,
		syncHealthy:        true,
	}
	server.SetReadinessChecker(checker)

	router := setupTestRouter(server)
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ready handler returned status %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp readyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if resp.Ready {
		t.Error("ready handler returned ready=true, want false")
	}
}

func TestReadyHandler_NotReady_SyncLag(t *testing.T) {
	server, _, _, _ := setupTestServer()

	checker := &mockReadinessChecker{
		peerDiscoveryReady: true,
		connectedPeers:     2,
		syncHealthy:        false,
		syncStatus: state.CRDTSyncStatus{
			PeersConnected: 2,
			SyncLagMs:      45000,
			LastSync:       time.Now().Add(-45 * time.Second),
		},
	}
	server.SetReadinessChecker(checker)

	router := setupTestRouter(server)
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ready handler returned status %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp readyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if resp.Ready {
		t.Error("ready handler returned ready=true, want false")
	}
	if resp.CRDTSync.SyncLagMs != 45000 {
		t.Errorf("sync_lag_ms = %d, want 45000", resp.CRDTSync.SyncLagMs)
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
		t.Errorf("ready endpoint not registered properly, got status %d: %s", w.Code, w.Body.String())
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

// Tests for Backup IP Pre-allocation Feature

func TestCreatePool_WithBackupRatio(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{
		"id": "pool1",
		"cidr": "192.168.0.0/24",
		"prefix": 28,
		"backup_ratio": 0.1
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

	if response.BackupRatio != 0.1 {
		t.Errorf("createPool BackupRatio = %f, want 0.1", response.BackupRatio)
	}
}

func TestCreatePool_InvalidBackupRatio(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{
		"id": "pool1",
		"cidr": "192.168.0.0/24",
		"prefix": 28,
		"backup_ratio": 1.5
	}`
	req := httptest.NewRequest("POST", "/api/v1/pools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createPool with invalid backup_ratio returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAllocation_WithNodeID(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool first
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}
	server.ring.AddPool("pool1", cidr)

	body := `{"pool_id": "pool1", "subscriber_id": "sub1", "node_id": "bng-node-1"}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("createAllocation returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response AllocationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.NodeID != "bng-node-1" {
		t.Errorf("createAllocation NodeID = %s, want bng-node-1", response.NodeID)
	}
}

func TestCreateAllocation_InvalidNodeID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"pool_id": "pool1", "subscriber_id": "sub1", "node_id": "invalid--node"}`
	req := httptest.NewRequest("POST", "/api/v1/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createAllocation with invalid node_id returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateBackupAllocations_Success(t *testing.T) {
	server, poolStore, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool with backup ratio
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:          "pool1",
		CIDR:        *cidr,
		Prefix:      28,
		BackupRatio: 0.5,
	}

	// Add some allocations owned by bng-node-1
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
		t.Errorf("createBackupAllocations returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response BackupAllocationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.BackupNodeID != "bng-node-2" {
		t.Errorf("createBackupAllocations BackupNodeID = %s, want bng-node-2", response.BackupNodeID)
	}
	// With 50% backup ratio and 2 allocations, we should assign 1 backup
	if response.AllocationsCount != 1 {
		t.Errorf("createBackupAllocations AllocationsCount = %d, want 1", response.AllocationsCount)
	}
}

func TestCreateBackupAllocations_InvalidBackupNodeID(t *testing.T) {
	server, poolStore, _, _ := setupTestServer()
	router := setupTestRouter(server)

	// Add a pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	poolStore.pools["pool1"] = &store.Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

	body := `{"backup_node_id": ""}`
	req := httptest.NewRequest("POST", "/api/v1/pools/pool1/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createBackupAllocations with empty backup_node_id returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateBackupAllocations_PoolNotFound(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	body := `{"backup_node_id": "bng-node-2"}`
	req := httptest.NewRequest("POST", "/api/v1/pools/nonexistent/backup-allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("createBackupAllocations for nonexistent pool returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestListBackupAllocations_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add an allocation with a backup node
	allocStore.allocations["sub1"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
		BackupNodeID: "bng-node-2",
	}

	req := httptest.NewRequest("GET", "/api/v1/nodes/bng-node-2/backup-allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listBackupAllocations returned status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 1 {
		t.Errorf("Expected count 1, got %v", response["count"])
	}

	backupNodeID, ok := response["backup_node_id"].(string)
	if !ok || backupNodeID != "bng-node-2" {
		t.Errorf("Expected backup_node_id bng-node-2, got %v", response["backup_node_id"])
	}
}

func TestListBackupAllocations_InvalidNodeID(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)

	req := httptest.NewRequest("GET", "/api/v1/nodes/--invalid/backup-allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("listBackupAllocations with invalid node_id returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestListNodeAllocations_Success(t *testing.T) {
	server, _, _, allocStore := setupTestServer()
	router := setupTestRouter(server)

	// Add allocations owned by bng-node-1
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
	// Add an allocation owned by a different node
	allocStore.allocations["sub3"] = &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub3",
		IP:           net.ParseIP("192.168.0.3"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-2",
	}

	req := httptest.NewRequest("GET", "/api/v1/nodes/bng-node-1/allocations", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("listNodeAllocations returned status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 2 {
		t.Errorf("Expected count 2, got %v", response["count"])
	}

	nodeID, ok := response["node_id"].(string)
	if !ok || nodeID != "bng-node-1" {
		t.Errorf("Expected node_id bng-node-1, got %v", response["node_id"])
	}
}

func TestAllocationToResponse_WithBackupFields(t *testing.T) {
	timestamp := time.Now()
	alloc := &store.Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    timestamp,
		NodeID:       "bng-node-1",
		BackupNodeID: "bng-node-2",
		IsBackup:     false,
	}

	response := allocationToResponse(alloc)

	if response.NodeID != "bng-node-1" {
		t.Errorf("allocationToResponse NodeID = %s, want bng-node-1", response.NodeID)
	}
	if response.BackupNodeID != "bng-node-2" {
		t.Errorf("allocationToResponse BackupNodeID = %s, want bng-node-2", response.BackupNodeID)
	}
	if response.IsBackup != false {
		t.Errorf("allocationToResponse IsBackup = %v, want false", response.IsBackup)
	}
}

func TestPoolToResponse_WithBackupRatio(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &store.Pool{
		ID:          "pool1",
		CIDR:        *cidr,
		Prefix:      28,
		BackupRatio: 0.15,
	}

	response := poolToResponse(pool)

	if response.BackupRatio != 0.15 {
		t.Errorf("poolToResponse BackupRatio = %f, want 0.15", response.BackupRatio)
	}
}
