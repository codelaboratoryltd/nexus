package failure

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// mockNodeStore implements NodeStore for testing.
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
	// Return a copy to prevent test interference
	result := make([]*store.Node, len(m.nodes))
	copy(result, m.nodes)
	return result, nil
}

func (m *mockNodeStore) SetNodes(nodes []*store.Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = nodes
}

func (m *mockNodeStore) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Helper to create a node with a specific best_before time.
func createNode(id string, bestBefore time.Time) *store.Node {
	return &store.Node{
		ID:         id,
		BestBefore: bestBefore,
		Metadata:   map[string]string{"name": "Nexus"},
	}
}

func TestNewDetector(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := NewDetector(nodeStore)

	if detector == nil {
		t.Fatal("NewDetector returned nil")
	}

	if detector.store != nodeStore {
		t.Error("Detector store was not set correctly")
	}

	// Check default config
	if detector.config.CheckInterval != DefaultCheckInterval {
		t.Errorf("Default CheckInterval = %v, want %v", detector.config.CheckInterval, DefaultCheckInterval)
	}
	if detector.config.FailureTimeout != DefaultFailureTimeout {
		t.Errorf("Default FailureTimeout = %v, want %v", detector.config.FailureTimeout, DefaultFailureTimeout)
	}
}

func TestNewDetectorWithOptions(t *testing.T) {
	nodeStore := newMockNodeStore()
	customConfig := Config{
		CheckInterval:  5 * time.Second,
		FailureTimeout: 15 * time.Second,
		GracePeriod:    3 * time.Second,
	}

	callback := func(event NodeFailedEvent) {
		// Callback provided to test option setting
		_ = event
	}

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	detector := NewDetector(nodeStore,
		WithConfig(customConfig),
		WithMetrics(metrics),
		WithCallback(callback),
	)

	if detector.config.CheckInterval != customConfig.CheckInterval {
		t.Errorf("CheckInterval = %v, want %v", detector.config.CheckInterval, customConfig.CheckInterval)
	}
	if detector.config.FailureTimeout != customConfig.FailureTimeout {
		t.Errorf("FailureTimeout = %v, want %v", detector.config.FailureTimeout, customConfig.FailureTimeout)
	}
	if detector.metrics != metrics {
		t.Error("Metrics was not set correctly")
	}
	if detector.onNodeFailed == nil {
		t.Error("Callback was not set")
	}
}

func TestDetectorStartStop(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := NewDetector(nodeStore, WithConfig(Config{
		CheckInterval:  50 * time.Millisecond,
		FailureTimeout: 100 * time.Millisecond,
		GracePeriod:    10 * time.Millisecond,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the detector
	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !detector.IsRunning() {
		t.Error("Detector should be running after Start")
	}

	// Starting again should be a no-op
	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}

	// Stop the detector
	detector.Stop()

	// Give it time to stop
	time.Sleep(100 * time.Millisecond)

	if detector.IsRunning() {
		t.Error("Detector should not be running after Stop")
	}

	// Stopping again should be a no-op
	detector.Stop()
}

func TestDetectorContextCancellation(t *testing.T) {
	nodeStore := newMockNodeStore()
	detector := NewDetector(nodeStore, WithConfig(Config{
		CheckInterval:  50 * time.Millisecond,
		FailureTimeout: 100 * time.Millisecond,
		GracePeriod:    10 * time.Millisecond,
	}))

	ctx, cancel := context.WithCancel(context.Background())

	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !detector.IsRunning() {
		t.Error("Detector should be running")
	}

	// Cancel context
	cancel()

	// Give it time to react
	time.Sleep(100 * time.Millisecond)

	if detector.IsRunning() {
		t.Error("Detector should stop when context is cancelled")
	}
}

func TestDetectorDetectsExpiredNode(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create a node that's already expired
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0, // No additional timeout - fail immediately when expired
			GracePeriod:    0, // No grace period
		}),
		WithCallback(callback),
	)

	ctx := context.Background()

	// Do a single check
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	if eventCount != 1 {
		t.Errorf("Expected 1 failure event, got %d", eventCount)
	}

	mu.Lock()
	if len(failedEvents) > 0 {
		event := failedEvents[0]
		if event.NodeID != "node1" {
			t.Errorf("Expected NodeID 'node1', got '%s'", event.NodeID)
		}
		if event.TimeSinceDue < time.Minute {
			t.Errorf("Expected TimeSinceDue >= 1 minute, got %v", event.TimeSinceDue)
		}
	}
	mu.Unlock()
}

func TestDetectorDoesNotEmitDuplicateFailures(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create an expired node
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithCallback(callback),
	)

	ctx := context.Background()

	// Do multiple checks
	detector.CheckNow(ctx)
	detector.CheckNow(ctx)
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	// Should only get one event, not three
	if eventCount != 1 {
		t.Errorf("Expected 1 failure event (no duplicates), got %d", eventCount)
	}
}

func TestDetectorResetsStateWhenNodeRecovers(t *testing.T) {
	nodeStore := newMockNodeStore()

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithCallback(callback),
	)

	ctx := context.Background()

	// Start with expired node
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})
	detector.CheckNow(ctx)

	mu.Lock()
	if len(failedEvents) != 1 {
		t.Errorf("Expected 1 failure event, got %d", len(failedEvents))
	}
	mu.Unlock()

	// Node recovers (best_before is in the future)
	recoveredNode := createNode("node1", time.Now().Add(1*time.Hour))
	nodeStore.SetNodes([]*store.Node{recoveredNode})
	detector.CheckNow(ctx)

	// Node expires again
	expiredAgain := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredAgain})
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	// Should get a second failure event since the node recovered in between
	if eventCount != 2 {
		t.Errorf("Expected 2 failure events after recovery and re-expiration, got %d", eventCount)
	}
}

func TestDetectorGracePeriod(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create a node that just expired
	expiredNode := createNode("node1", time.Now().Add(-10*time.Millisecond))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 1 * time.Hour, // Long timeout - we're testing grace period
			GracePeriod:    100 * time.Millisecond,
		}),
		WithCallback(callback),
	)

	ctx := context.Background()

	// First check - should not fail yet (grace period not exceeded)
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	if eventCount != 0 {
		t.Errorf("Expected 0 failure events before grace period, got %d", eventCount)
	}

	// Wait for grace period to pass
	time.Sleep(150 * time.Millisecond)

	// Second check - should now detect failure
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount = len(failedEvents)
	mu.Unlock()

	if eventCount != 1 {
		t.Errorf("Expected 1 failure event after grace period, got %d", eventCount)
	}
}

func TestDetectorSubscription(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create an expired node
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
	)

	// Create subscriber channels
	ch1 := make(chan NodeFailedEvent, 10)
	ch2 := make(chan NodeFailedEvent, 10)

	detector.Subscribe(ch1)
	detector.Subscribe(ch2)

	ctx := context.Background()
	detector.CheckNow(ctx)

	// Both channels should receive the event
	select {
	case event := <-ch1:
		if event.NodeID != "node1" {
			t.Errorf("ch1: Expected NodeID 'node1', got '%s'", event.NodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch1: Did not receive event")
	}

	select {
	case event := <-ch2:
		if event.NodeID != "node1" {
			t.Errorf("ch2: Expected NodeID 'node1', got '%s'", event.NodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2: Did not receive event")
	}

	// Unsubscribe ch1
	detector.Unsubscribe(ch1)

	// Reset node state and make it fail again
	detector.ResetNodeState("node1")
	detector.CheckNow(ctx)

	// Only ch2 should receive the second event
	select {
	case <-ch1:
		t.Error("ch1 should not receive event after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// Expected - ch1 is unsubscribed
	}

	select {
	case event := <-ch2:
		if event.NodeID != "node1" {
			t.Errorf("ch2: Expected NodeID 'node1', got '%s'", event.NodeID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2: Did not receive second event")
	}
}

func TestDetectorMetrics(t *testing.T) {
	nodeStore := newMockNodeStore()

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Create nodes - one healthy, one expired
	healthyNode := createNode("healthy", time.Now().Add(1*time.Hour))
	expiredNode := createNode("expired", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{healthyNode, expiredNode})

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithMetrics(metrics),
	)

	ctx := context.Background()
	detector.CheckNow(ctx)

	// Gather metrics
	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check metrics values
	metricsMap := make(map[string]float64)
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			switch *mf.Name {
			case "nexus_failure_detector_nodes_monitored":
				metricsMap["nodes_monitored"] = m.GetGauge().GetValue()
			case "nexus_failure_detector_nodes_healthy":
				metricsMap["nodes_healthy"] = m.GetGauge().GetValue()
			case "nexus_failure_detector_nodes_expired":
				metricsMap["nodes_expired"] = m.GetGauge().GetValue()
			case "nexus_failure_detector_nodes_failed_total":
				metricsMap["nodes_failed_total"] = m.GetCounter().GetValue()
			}
		}
	}

	if metricsMap["nodes_monitored"] != 2 {
		t.Errorf("nodes_monitored = %v, want 2", metricsMap["nodes_monitored"])
	}
	if metricsMap["nodes_healthy"] != 1 {
		t.Errorf("nodes_healthy = %v, want 1", metricsMap["nodes_healthy"])
	}
	if metricsMap["nodes_expired"] != 1 {
		t.Errorf("nodes_expired = %v, want 1", metricsMap["nodes_expired"])
	}
	if metricsMap["nodes_failed_total"] != 1 {
		t.Errorf("nodes_failed_total = %v, want 1", metricsMap["nodes_failed_total"])
	}
}

func TestDetectorHandlesStoreError(t *testing.T) {
	nodeStore := newMockNodeStore()
	nodeStore.SetError(store.ErrPoolNotFound) // Any error

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
	)

	ctx := context.Background()

	// Should not panic when store returns error
	detector.CheckNow(ctx)
}

func TestDetectorCleansUpRemovedNodes(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Start with two nodes
	node1 := createNode("node1", time.Now().Add(1*time.Hour))
	node2 := createNode("node2", time.Now().Add(1*time.Hour))
	nodeStore.SetNodes([]*store.Node{node1, node2})

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
	)

	ctx := context.Background()
	detector.CheckNow(ctx)

	// Check that both nodes are tracked
	if state := detector.GetNodeState("node1"); state == nil {
		t.Error("node1 should be tracked")
	}
	if state := detector.GetNodeState("node2"); state == nil {
		t.Error("node2 should be tracked")
	}

	// Remove node2
	nodeStore.SetNodes([]*store.Node{node1})
	detector.CheckNow(ctx)

	// node2 should be cleaned up
	if state := detector.GetNodeState("node2"); state != nil {
		t.Error("node2 should be cleaned up after removal")
	}
	if state := detector.GetNodeState("node1"); state == nil {
		t.Error("node1 should still be tracked")
	}
}

func TestDetectorResetNodeState(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create an expired node
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithCallback(callback),
	)

	ctx := context.Background()

	// First check
	detector.CheckNow(ctx)

	mu.Lock()
	if len(failedEvents) != 1 {
		t.Errorf("Expected 1 failure event, got %d", len(failedEvents))
	}
	mu.Unlock()

	// Reset the state
	detector.ResetNodeState("node1")

	// Check again - should detect failure again
	detector.CheckNow(ctx)

	mu.Lock()
	if len(failedEvents) != 2 {
		t.Errorf("Expected 2 failure events after reset, got %d", len(failedEvents))
	}
	mu.Unlock()
}

func TestDetectorMultipleNodes(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create mix of healthy and expired nodes
	healthy1 := createNode("healthy1", time.Now().Add(1*time.Hour))
	healthy2 := createNode("healthy2", time.Now().Add(2*time.Hour))
	expired1 := createNode("expired1", time.Now().Add(-1*time.Minute))
	expired2 := createNode("expired2", time.Now().Add(-2*time.Minute))

	nodeStore.SetNodes([]*store.Node{healthy1, healthy2, expired1, expired2})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithCallback(callback),
	)

	ctx := context.Background()
	detector.CheckNow(ctx)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	// Should detect both expired nodes
	if eventCount != 2 {
		t.Errorf("Expected 2 failure events, got %d", eventCount)
	}

	// Verify the failed nodes
	mu.Lock()
	failedNodeIDs := make(map[string]bool)
	for _, e := range failedEvents {
		failedNodeIDs[e.NodeID] = true
	}
	mu.Unlock()

	if !failedNodeIDs["expired1"] {
		t.Error("expired1 should be in failed events")
	}
	if !failedNodeIDs["expired2"] {
		t.Error("expired2 should be in failed events")
	}
	if failedNodeIDs["healthy1"] {
		t.Error("healthy1 should not be in failed events")
	}
	if failedNodeIDs["healthy2"] {
		t.Error("healthy2 should not be in failed events")
	}
}

func TestDetectorRunningIntegration(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Start with healthy node
	healthyNode := createNode("node1", time.Now().Add(200*time.Millisecond))
	nodeStore.SetNodes([]*store.Node{healthyNode})

	var failedEvents []NodeFailedEvent
	var mu sync.Mutex

	callback := func(event NodeFailedEvent) {
		mu.Lock()
		defer mu.Unlock()
		failedEvents = append(failedEvents, event)
	}

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  50 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
		WithCallback(callback),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := detector.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for node to expire
	time.Sleep(350 * time.Millisecond)

	mu.Lock()
	eventCount := len(failedEvents)
	mu.Unlock()

	if eventCount == 0 {
		t.Error("Expected failure event after node expired")
	}

	detector.Stop()
}

func TestDetectorSubscriberFullChannel(t *testing.T) {
	nodeStore := newMockNodeStore()

	// Create an expired node
	expiredNode := createNode("node1", time.Now().Add(-1*time.Minute))
	nodeStore.SetNodes([]*store.Node{expiredNode})

	detector := NewDetector(nodeStore,
		WithConfig(Config{
			CheckInterval:  10 * time.Millisecond,
			FailureTimeout: 0,
			GracePeriod:    0,
		}),
	)

	// Create a channel with no buffer
	ch := make(chan NodeFailedEvent)
	detector.Subscribe(ch)

	ctx := context.Background()

	// This should not block even though channel is full
	done := make(chan bool)
	go func() {
		detector.CheckNow(ctx)
		done <- true
	}()

	select {
	case <-done:
		// Good - didn't block
	case <-time.After(1 * time.Second):
		t.Error("CheckNow blocked on full subscriber channel")
	}
}

func TestNewMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	if metrics == nil {
		t.Fatal("NewMetrics returned nil")
	}

	if metrics.NodesFailedTotal == nil {
		t.Error("NodesFailedTotal is nil")
	}
	if metrics.DetectionLatency == nil {
		t.Error("DetectionLatency is nil")
	}
	if metrics.NodesMonitored == nil {
		t.Error("NodesMonitored is nil")
	}
	if metrics.NodesHealthy == nil {
		t.Error("NodesHealthy is nil")
	}
	if metrics.NodesExpired == nil {
		t.Error("NodesExpired is nil")
	}
	if metrics.CheckDuration == nil {
		t.Error("CheckDuration is nil")
	}
}

func TestNewMetricsNilRegistry(t *testing.T) {
	// Should work with nil registry (just doesn't register)
	metrics := NewMetrics(nil)
	if metrics == nil {
		t.Fatal("NewMetrics with nil registry returned nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.CheckInterval != DefaultCheckInterval {
		t.Errorf("CheckInterval = %v, want %v", cfg.CheckInterval, DefaultCheckInterval)
	}
	if cfg.FailureTimeout != DefaultFailureTimeout {
		t.Errorf("FailureTimeout = %v, want %v", cfg.FailureTimeout, DefaultFailureTimeout)
	}
	if cfg.GracePeriod != DefaultGracePeriod {
		t.Errorf("GracePeriod = %v, want %v", cfg.GracePeriod, DefaultGracePeriod)
	}
}
