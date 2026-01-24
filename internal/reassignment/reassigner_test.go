package reassignment

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/failure"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// mockNodeStore implements failure.NodeStore for testing.
type mockNodeStore struct {
	mu    sync.Mutex
	nodes []*store.Node
	err   error
}

func newMockNodeStore() *mockNodeStore {
	return &mockNodeStore{
		nodes: make([]*store.Node, 0),
	}
}

func (m *mockNodeStore) ListNodes(ctx context.Context) ([]*store.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	result := make([]*store.Node, len(m.nodes))
	copy(result, m.nodes)
	return result, nil
}

func (m *mockNodeStore) SetNodes(nodes []*store.Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = nodes
}

// Helper to create a node with a specific best_before time.
func createNode(id string, bestBefore time.Time) *store.Node {
	return &store.Node{
		ID:         id,
		BestBefore: bestBefore,
		Metadata:   map[string]string{"name": "Nexus"},
	}
}

// Helper to set up a test hashring with nodes and a pool.
func setupTestHashring(nodeIDs []string, poolCIDR string) *hashring.VirtualHashRing {
	ring := hashring.NewVirtualNodesHashRing()

	// Add nodes
	for _, nodeID := range nodeIDs {
		_ = ring.AddNode(hashring.NodeID(nodeID))
	}

	// Add pool if specified
	if poolCIDR != "" {
		_, network, _ := net.ParseCIDR(poolCIDR)
		_ = ring.RegisterPool(hashring.IPPool{
			ID:          "test-pool",
			Network:     network,
			VNodesCount: 12, // Use 12 vnodes to ensure each node gets at least some partitions
		})
	}

	return ring
}

func TestNewReassigner(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	if reassigner == nil {
		t.Fatal("NewReassigner returned nil")
	}

	if reassigner.hashring != ring {
		t.Error("Hashring was not set correctly")
	}

	if reassigner.detector != detector {
		t.Error("Detector was not set correctly")
	}
}

func TestReassignerStartStop(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the reassigner
	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !reassigner.IsRunning() {
		t.Error("Reassigner should be running after Start")
	}

	// Starting again should be a no-op
	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}

	// Stop the reassigner
	reassigner.Stop()

	// Give it time to stop
	time.Sleep(50 * time.Millisecond)

	if reassigner.IsRunning() {
		t.Error("Reassigner should not be running after Stop")
	}

	// Stopping again should be a no-op
	reassigner.Stop()
}

func TestReassignerContextCancellation(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	ctx, cancel := context.WithCancel(context.Background())

	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !reassigner.IsRunning() {
		t.Error("Reassigner should be running")
	}

	// Cancel context
	cancel()

	// Give it time to react
	time.Sleep(100 * time.Millisecond)

	if reassigner.IsRunning() {
		t.Error("Reassigner should stop when context is cancelled")
	}
}

func TestReassignerNodeFailureTriggersReassignment(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Set up nodes - one will expire
	healthyNode := createNode("node1", time.Now().Add(1*time.Hour))
	expiringNode := createNode("node2", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{healthyNode, expiringNode})

	detector := failure.NewDetector(nodeStore, failure.WithConfig(failure.Config{
		CheckInterval:  10 * time.Millisecond,
		FailureTimeout: 0,
		GracePeriod:    0,
	}))

	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	var reassignmentEvents []ReassignmentEvent
	var mu sync.Mutex

	callback := func(event ReassignmentEvent) {
		mu.Lock()
		defer mu.Unlock()
		reassignmentEvents = append(reassignmentEvents, event)
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start both detector and reassigner
	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Detector start failed: %v", err)
	}
	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Reassigner start failed: %v", err)
	}

	// Wait for failure detection and reassignment
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	eventCount := len(reassignmentEvents)
	mu.Unlock()

	if eventCount != 1 {
		t.Errorf("Expected 1 reassignment event, got %d", eventCount)
	}

	mu.Lock()
	if len(reassignmentEvents) > 0 {
		event := reassignmentEvents[0]
		if event.FailedNodeID != "node2" {
			t.Errorf("Expected FailedNodeID 'node2', got '%s'", event.FailedNodeID)
		}
	}
	mu.Unlock()

	// Verify node was removed from hashring
	nodes := ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node remaining, got %d", len(nodes))
	}

	detector.Stop()
	reassigner.Stop()
}

func TestReassignerPartitionsRedistributed(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore, failure.WithConfig(failure.Config{
		CheckInterval:  10 * time.Millisecond,
		FailureTimeout: 0,
		GracePeriod:    0,
	}))

	ring := setupTestHashring([]string{"node1", "node2", "node3"}, "10.0.0.0/24")

	// Get partitions owned by node2 before failure
	mappingBefore := ring.GetHashMapping()
	node2Partitions := mappingBefore.ListOwnedVirtualHashes(hashring.NodeID("node2"))
	node2PartitionCount := 0
	for _, vnodes := range node2Partitions {
		node2PartitionCount += len(vnodes)
	}

	var reassignmentEvent ReassignmentEvent
	var mu sync.Mutex
	eventReceived := make(chan struct{})

	callback := func(event ReassignmentEvent) {
		mu.Lock()
		defer mu.Unlock()
		reassignmentEvent = event
		close(eventReceived)
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	// Manually trigger a node failure
	reassigner.HandleNodeFailureNow("node2")

	select {
	case <-eventReceived:
		// Good
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive reassignment event")
	}

	mu.Lock()
	// Verify partitions were moved
	totalMoved := 0
	for _, partitions := range reassignmentEvent.PartitionsMoved {
		totalMoved += len(partitions)
	}
	mu.Unlock()

	if totalMoved == 0 && node2PartitionCount > 0 {
		t.Error("No partitions were moved during reassignment")
	}

	// Verify all moved partitions have new owners
	mu.Lock()
	for poolID, partitions := range reassignmentEvent.PartitionsMoved {
		for _, partition := range partitions {
			if partition.OldOwner != hashring.NodeID("node2") {
				t.Errorf("Partition %s had unexpected old owner %s, expected node2",
					partition.VirtualNodeID, partition.OldOwner)
			}
			if partition.NewOwner == hashring.NodeID("node2") {
				t.Errorf("Partition %s still owned by failed node2 (pool %s)",
					partition.VirtualNodeID, poolID)
			}
		}
	}
	mu.Unlock()

	// Verify node was removed
	nodes := ring.ListNodes()
	for _, n := range nodes {
		if n == hashring.NodeID("node2") {
			t.Error("node2 should have been removed from hashring")
		}
	}
}

func TestReassignerMultipleSimultaneousFailures(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Set up nodes - two will expire
	healthyNode := createNode("node1", time.Now().Add(1*time.Hour))
	expiringNode1 := createNode("node2", time.Now().Add(-1*time.Minute))
	expiringNode2 := createNode("node3", time.Now().Add(-2*time.Minute))
	nodeStore.SetNodes([]*store.Node{healthyNode, expiringNode1, expiringNode2})

	detector := failure.NewDetector(nodeStore, failure.WithConfig(failure.Config{
		CheckInterval:  10 * time.Millisecond,
		FailureTimeout: 0,
		GracePeriod:    0,
	}))

	ring := setupTestHashring([]string{"node1", "node2", "node3"}, "10.0.0.0/24")

	var reassignmentEvents []ReassignmentEvent
	var mu sync.Mutex

	callback := func(event ReassignmentEvent) {
		mu.Lock()
		defer mu.Unlock()
		reassignmentEvents = append(reassignmentEvents, event)
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start both detector and reassigner
	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Detector start failed: %v", err)
	}
	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Reassigner start failed: %v", err)
	}

	// Wait for failure detection and reassignment of both nodes
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	eventCount := len(reassignmentEvents)
	mu.Unlock()

	if eventCount != 2 {
		t.Errorf("Expected 2 reassignment events for 2 failed nodes, got %d", eventCount)
	}

	// Verify only one node remains
	nodes := ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node remaining, got %d", len(nodes))
	}

	if len(nodes) > 0 && nodes[0] != hashring.NodeID("node1") {
		t.Errorf("Expected remaining node to be node1, got %s", nodes[0])
	}

	detector.Stop()
	reassigner.Stop()
}

func TestReassignerMetrics(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2", "node3"}, "10.0.0.0/24")

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
		Metrics:  metrics,
	})

	// Get initial partition count - find a node that has partitions
	mappingBefore := ring.GetHashMapping()

	// Find a node with partitions to fail
	failNode := ""
	for _, nodeID := range []string{"node1", "node2", "node3"} {
		partitions := mappingBefore.ListOwnedVirtualHashes(hashring.NodeID(nodeID))
		count := 0
		for _, vnodes := range partitions {
			count += len(vnodes)
		}
		if count > 0 && failNode == "" {
			failNode = nodeID
		}
	}
	if failNode == "" {
		t.Fatal("No node has partitions")
	}

	// Trigger a reassignment for a node that has partitions
	reassigner.HandleNodeFailureNow(failNode)

	// Gather metrics
	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	metricsMap := make(map[string]float64)
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			switch *mf.Name {
			case "nexus_reassignment_total":
				metricsMap["total"] = m.GetCounter().GetValue()
			case "nexus_reassignment_partitions_moved_total":
				metricsMap["partitions_moved"] = m.GetCounter().GetValue()
			case "nexus_reassignment_active_nodes":
				metricsMap["active_nodes"] = m.GetGauge().GetValue()
			}
		}
	}

	if metricsMap["total"] != 1 {
		t.Errorf("reassignment_total = %v, want 1", metricsMap["total"])
	}

	if metricsMap["partitions_moved"] < 1 {
		t.Errorf("partitions_moved = %v, want >= 1", metricsMap["partitions_moved"])
	}

	if metricsMap["active_nodes"] != 2 {
		t.Errorf("active_nodes = %v, want 2", metricsMap["active_nodes"])
	}
}

func TestReassignerSubscription(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	// Create subscriber channels
	ch1 := make(chan ReassignmentEvent, 10)
	ch2 := make(chan ReassignmentEvent, 10)

	reassigner.Subscribe(ch1)
	reassigner.Subscribe(ch2)

	// Trigger reassignment
	reassigner.HandleNodeFailureNow("node2")

	// Both channels should receive the event
	select {
	case event := <-ch1:
		if event.FailedNodeID != "node2" {
			t.Errorf("ch1: Expected FailedNodeID 'node2', got '%s'", event.FailedNodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch1: Did not receive event")
	}

	select {
	case event := <-ch2:
		if event.FailedNodeID != "node2" {
			t.Errorf("ch2: Expected FailedNodeID 'node2', got '%s'", event.FailedNodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2: Did not receive event")
	}

	// Unsubscribe ch1
	reassigner.Unsubscribe(ch1)

	// Add node back and fail again
	_ = ring.AddNode(hashring.NodeID("node2"))
	reassigner.HandleNodeFailureNow("node2")

	// Only ch2 should receive the second event
	select {
	case <-ch1:
		t.Error("ch1 should not receive event after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// Expected - ch1 is unsubscribed
	}

	select {
	case event := <-ch2:
		if event.FailedNodeID != "node2" {
			t.Errorf("ch2: Expected FailedNodeID 'node2', got '%s'", event.FailedNodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2: Did not receive second event")
	}
}

func TestReassignerFullSubscriberChannel(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	// Create a channel with no buffer
	ch := make(chan ReassignmentEvent)
	reassigner.Subscribe(ch)

	// This should not block even though channel is full
	done := make(chan bool)
	go func() {
		reassigner.HandleNodeFailureNow("node2")
		done <- true
	}()

	select {
	case <-done:
		// Good - didn't block
	case <-time.After(1 * time.Second):
		t.Error("HandleNodeFailureNow blocked on full subscriber channel")
	}
}

func TestReassignerNoPoolsNoPartitions(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)

	// Create ring with nodes but no pools
	ring := hashring.NewVirtualNodesHashRing()
	_ = ring.AddNode(hashring.NodeID("node1"))
	_ = ring.AddNode(hashring.NodeID("node2"))

	var reassignmentEvent ReassignmentEvent
	eventReceived := make(chan struct{})

	callback := func(event ReassignmentEvent) {
		reassignmentEvent = event
		close(eventReceived)
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	// Trigger reassignment
	reassigner.HandleNodeFailureNow("node2")

	select {
	case <-eventReceived:
		// Good
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive reassignment event")
	}

	// Should succeed but with no partitions moved
	totalMoved := 0
	for _, partitions := range reassignmentEvent.PartitionsMoved {
		totalMoved += len(partitions)
	}

	if totalMoved != 0 {
		t.Errorf("Expected 0 partitions moved when no pools, got %d", totalMoved)
	}

	// Node should still be removed
	nodes := ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node remaining, got %d", len(nodes))
	}
}

func TestReassignerSingleNodeFailure(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)

	// Create ring with only one node
	ring := hashring.NewVirtualNodesHashRing()
	_ = ring.AddNode(hashring.NodeID("node1"))

	_, network, _ := net.ParseCIDR("10.0.0.0/24")
	_ = ring.RegisterPool(hashring.IPPool{
		ID:          "test-pool",
		Network:     network,
		VNodesCount: 4,
	})

	var reassignmentEvent ReassignmentEvent
	eventReceived := make(chan struct{})

	callback := func(event ReassignmentEvent) {
		reassignmentEvent = event
		close(eventReceived)
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	// Trigger reassignment of the only node
	reassigner.HandleNodeFailureNow("node1")

	select {
	case <-eventReceived:
		// Good
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive reassignment event")
	}

	// Note: When the last node is removed, the hashring doesn't clear the mapping
	// (updateVirtualHashRing returns early when nodes is empty), so the old mapping
	// persists. This is acceptable behavior - the ring is empty anyway.
	// We just verify that partitions were tracked during reassignment.
	totalMoved := 0
	for _, partitions := range reassignmentEvent.PartitionsMoved {
		totalMoved += len(partitions)
	}
	if totalMoved == 0 {
		t.Error("Expected at least one partition to be tracked during reassignment")
	}

	// Ring should be empty
	nodes := ring.ListNodes()
	if len(nodes) != 0 {
		t.Errorf("Expected 0 nodes remaining, got %d", len(nodes))
	}
}

func TestReassignerGetActiveNodeCount(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := failure.NewDetector(nodeStore)
	ring := setupTestHashring([]string{"node1", "node2", "node3"}, "10.0.0.0/24")

	reassigner := NewReassigner(Config{
		Hashring: ring,
		Detector: detector,
	})

	if count := reassigner.GetActiveNodeCount(); count != 3 {
		t.Errorf("Expected 3 active nodes, got %d", count)
	}

	// Remove a node
	reassigner.HandleNodeFailureNow("node2")

	if count := reassigner.GetActiveNodeCount(); count != 2 {
		t.Errorf("Expected 2 active nodes after failure, got %d", count)
	}
}

func TestNewMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	if metrics == nil {
		t.Fatal("NewMetrics returned nil")
	}

	if metrics.ReassignmentsTotal == nil {
		t.Error("ReassignmentsTotal is nil")
	}
	if metrics.ReassignmentLatency == nil {
		t.Error("ReassignmentLatency is nil")
	}
	if metrics.PartitionsMoved == nil {
		t.Error("PartitionsMoved is nil")
	}
	if metrics.ActiveNodes == nil {
		t.Error("ActiveNodes is nil")
	}
}

func TestNewMetricsNilRegistry(t *testing.T) {
	// Should work with nil registry (just doesn't register)
	metrics := NewMetrics(nil)
	if metrics == nil {
		t.Fatal("NewMetrics with nil registry returned nil")
	}
}

func TestReassignerIntegrationWithFailureDetector(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Start with healthy node that will expire
	expiringNode := createNode("node1", time.Now().Add(100*time.Millisecond))
	healthyNode := createNode("node2", time.Now().Add(1*time.Hour))
	nodeStore.SetNodes([]*store.Node{expiringNode, healthyNode})

	detector := failure.NewDetector(nodeStore, failure.WithConfig(failure.Config{
		CheckInterval:  50 * time.Millisecond,
		FailureTimeout: 0,
		GracePeriod:    0,
	}))

	ring := setupTestHashring([]string{"node1", "node2"}, "10.0.0.0/24")

	var reassignmentEvent ReassignmentEvent
	var mu sync.Mutex
	eventReceived := make(chan struct{}, 1)

	callback := func(event ReassignmentEvent) {
		mu.Lock()
		defer mu.Unlock()
		reassignmentEvent = event
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	}

	reassigner := NewReassigner(Config{
		Hashring:       ring,
		Detector:       detector,
		OnReassignment: callback,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start both
	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Detector start failed: %v", err)
	}
	if err := reassigner.Start(ctx); err != nil {
		t.Fatalf("Reassigner start failed: %v", err)
	}

	// Wait for node to expire and be detected/reassigned
	select {
	case <-eventReceived:
		// Good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for reassignment event")
	}

	mu.Lock()
	if reassignmentEvent.FailedNodeID != "node1" {
		t.Errorf("Expected FailedNodeID 'node1', got '%s'", reassignmentEvent.FailedNodeID)
	}
	mu.Unlock()

	// Verify only node2 remains
	nodes := ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node remaining, got %d", len(nodes))
	}
	if len(nodes) > 0 && nodes[0] != hashring.NodeID("node2") {
		t.Errorf("Expected remaining node to be node2, got %s", nodes[0])
	}

	detector.Stop()
	reassigner.Stop()
}
