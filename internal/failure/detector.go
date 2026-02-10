// Package failure provides automatic detection of failed Nexus nodes in the cluster.
// It monitors node heartbeats and detects when nodes exceed their best_before time,
// triggering events for shard reassignment.
package failure

import (
	"context"
	"sync"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// DefaultCheckInterval is the default interval for checking node health.
	DefaultCheckInterval = 10 * time.Second

	// DefaultFailureTimeout is the default timeout after which a node is considered failed.
	// This is typically 3x the heartbeat interval (e.g., 10s heartbeat = 30s timeout).
	DefaultFailureTimeout = 30 * time.Second

	// DefaultGracePeriod is the time to wait before declaring a node failed after
	// it first exceeds its best_before time. This prevents flapping.
	DefaultGracePeriod = 5 * time.Second
)

// NodeFailedEvent represents an event emitted when a node failure is detected.
type NodeFailedEvent struct {
	NodeID       string
	BestBefore   time.Time
	DetectedAt   time.Time
	TimeSinceDue time.Duration // How long past best_before the node was
}

// NodeStore is the interface for accessing node information.
// This matches the store.NodeStore interface but is defined here for testability.
type NodeStore interface {
	ListNodes(ctx context.Context) ([]*store.Node, error)
}

// Config holds configuration for the failure detector.
type Config struct {
	// CheckInterval is how often to check for failed nodes.
	CheckInterval time.Duration

	// FailureTimeout is how long after best_before before a node is considered failed.
	// This provides a grace period for network delays.
	FailureTimeout time.Duration

	// GracePeriod is the time a node must be continuously past its best_before
	// before being declared failed. This prevents flapping.
	GracePeriod time.Duration
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		CheckInterval:  DefaultCheckInterval,
		FailureTimeout: DefaultFailureTimeout,
		GracePeriod:    DefaultGracePeriod,
	}
}

// Metrics holds Prometheus metrics for the failure detector.
type Metrics struct {
	// NodesFailedTotal is a counter of total node failures detected.
	NodesFailedTotal prometheus.Counter

	// DetectionLatency measures the time from best_before to failure detection.
	DetectionLatency prometheus.Histogram

	// NodesMonitored is a gauge of nodes currently being monitored.
	NodesMonitored prometheus.Gauge

	// NodesHealthy is a gauge of nodes currently healthy (not expired).
	NodesHealthy prometheus.Gauge

	// NodesExpired is a gauge of nodes currently expired but not yet failed.
	NodesExpired prometheus.Gauge

	// CheckDuration measures how long each check cycle takes.
	CheckDuration prometheus.Histogram
}

// NewMetrics creates and registers Prometheus metrics for the failure detector.
func NewMetrics(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		NodesFailedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "nodes_failed_total",
			Help:      "Total number of node failures detected.",
		}),
		DetectionLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "detection_latency_seconds",
			Help:      "Time from node best_before to failure detection.",
			Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
		}),
		NodesMonitored: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "nodes_monitored",
			Help:      "Number of nodes currently being monitored.",
		}),
		NodesHealthy: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "nodes_healthy",
			Help:      "Number of nodes currently healthy (not expired).",
		}),
		NodesExpired: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "nodes_expired",
			Help:      "Number of nodes currently expired but not yet failed.",
		}),
		CheckDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "nexus",
			Subsystem: "failure_detector",
			Name:      "check_duration_seconds",
			Help:      "Duration of each node health check cycle.",
			Buckets:   prometheus.DefBuckets,
		}),
	}

	if registerer != nil {
		registerer.MustRegister(
			m.NodesFailedTotal,
			m.DetectionLatency,
			m.NodesMonitored,
			m.NodesHealthy,
			m.NodesExpired,
			m.CheckDuration,
		)
	}

	return m
}

// nodeState tracks the state of a node for failure detection.
type nodeState struct {
	// firstExpiredAt is when the node was first observed as expired.
	// Used to implement grace period.
	firstExpiredAt time.Time

	// alreadyFailed indicates we've already emitted a failure event for this node.
	alreadyFailed bool
}

// Detector monitors node health and detects failures.
type Detector struct {
	mu sync.Mutex

	store   NodeStore
	config  Config
	metrics *Metrics

	// nodeStates tracks the state of each node for failure detection.
	nodeStates map[string]*nodeState

	// onNodeFailed is called when a node failure is detected.
	onNodeFailed func(event NodeFailedEvent)

	// subscribers is a list of channels to notify on node failure.
	subscribers []chan<- NodeFailedEvent

	// running indicates whether the detector is currently running.
	running bool

	// stopCh is used to signal the detector to stop.
	stopCh chan struct{}
}

// Option is a functional option for configuring the Detector.
type Option func(*Detector)

// WithConfig sets the detector configuration.
func WithConfig(cfg Config) Option {
	return func(d *Detector) {
		d.config = cfg
	}
}

// WithMetrics sets the Prometheus metrics for the detector.
func WithMetrics(m *Metrics) Option {
	return func(d *Detector) {
		d.metrics = m
	}
}

// WithCallback sets a callback function to be called when a node fails.
func WithCallback(fn func(event NodeFailedEvent)) Option {
	return func(d *Detector) {
		d.onNodeFailed = fn
	}
}

// NewDetector creates a new failure detector with the given node store and options.
func NewDetector(nodeStore NodeStore, opts ...Option) *Detector {
	d := &Detector{
		store:      nodeStore,
		config:     DefaultConfig(),
		nodeStates: make(map[string]*nodeState),
		stopCh:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// Subscribe adds a channel to receive failure events.
// The channel should be buffered to prevent blocking the detector.
func (d *Detector) Subscribe(ch chan<- NodeFailedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers = append(d.subscribers, ch)
}

// Unsubscribe removes a channel from receiving failure events.
func (d *Detector) Unsubscribe(ch chan<- NodeFailedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, sub := range d.subscribers {
		if sub == ch {
			d.subscribers = append(d.subscribers[:i], d.subscribers[i+1:]...)
			return
		}
	}
}

// Start begins the failure detection loop.
// It returns immediately and runs the detector in a background goroutine.
// The detector will run until Stop is called or the context is canceled.
func (d *Detector) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return nil
	}
	d.running = true
	d.stopCh = make(chan struct{})
	d.mu.Unlock()

	go d.run(ctx)
	return nil
}

// Stop stops the failure detection loop.
func (d *Detector) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.stopCh)
	d.mu.Unlock()
}

// IsRunning returns whether the detector is currently running.
func (d *Detector) IsRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// run is the main detection loop.
func (d *Detector) run(ctx context.Context) {
	ticker := time.NewTicker(d.config.CheckInterval)
	defer ticker.Stop()

	// Do an initial check immediately
	d.checkNodes(ctx)

	for {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.running = false
			d.mu.Unlock()
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.checkNodes(ctx)
		}
	}
}

// checkNodes checks all nodes and detects failures.
func (d *Detector) checkNodes(ctx context.Context) {
	startTime := time.Now()
	defer func() {
		if d.metrics != nil {
			d.metrics.CheckDuration.Observe(time.Since(startTime).Seconds())
		}
	}()

	nodes, err := d.store.ListNodes(ctx)
	if err != nil {
		// Log error but continue - don't want to crash the detector
		return
	}

	now := time.Now()
	healthy := 0
	expired := 0

	d.mu.Lock()
	defer d.mu.Unlock()

	// Track which nodes we've seen this check
	seenNodes := make(map[string]bool)

	for _, node := range nodes {
		seenNodes[node.ID] = true

		// Get or create node state
		state, exists := d.nodeStates[node.ID]
		if !exists {
			state = &nodeState{}
			d.nodeStates[node.ID] = state
		}

		// Check if node has exceeded best_before
		if now.After(node.BestBefore) {
			// Node is expired
			expired++

			// If this is the first time we've seen it expired, record the time
			if state.firstExpiredAt.IsZero() {
				state.firstExpiredAt = now
			}

			// Check if we should declare failure (past grace period and not already failed)
			gracePeriodExceeded := now.Sub(state.firstExpiredAt) >= d.config.GracePeriod
			timeoutExceeded := now.Sub(node.BestBefore) >= d.config.FailureTimeout

			if !state.alreadyFailed && (gracePeriodExceeded || timeoutExceeded) {
				// Declare node as failed
				state.alreadyFailed = true

				event := NodeFailedEvent{
					NodeID:       node.ID,
					BestBefore:   node.BestBefore,
					DetectedAt:   now,
					TimeSinceDue: now.Sub(node.BestBefore),
				}

				// Update metrics
				if d.metrics != nil {
					d.metrics.NodesFailedTotal.Inc()
					d.metrics.DetectionLatency.Observe(event.TimeSinceDue.Seconds())
				}

				// Emit event
				d.emitFailure(event)
			}
		} else {
			// Node is healthy - reset state
			healthy++
			state.firstExpiredAt = time.Time{}
			state.alreadyFailed = false
		}
	}

	// Clean up state for nodes that no longer exist
	for nodeID := range d.nodeStates {
		if !seenNodes[nodeID] {
			delete(d.nodeStates, nodeID)
		}
	}

	// Update metrics
	if d.metrics != nil {
		d.metrics.NodesMonitored.Set(float64(len(nodes)))
		d.metrics.NodesHealthy.Set(float64(healthy))
		d.metrics.NodesExpired.Set(float64(expired))
	}
}

// emitFailure sends the failure event to all subscribers and callback.
// Must be called with d.mu held.
func (d *Detector) emitFailure(event NodeFailedEvent) {
	// Call callback if set
	if d.onNodeFailed != nil {
		// Release lock for callback to prevent deadlocks
		d.mu.Unlock()
		d.onNodeFailed(event)
		d.mu.Lock()
	}

	// Notify all subscribers (non-blocking)
	for _, ch := range d.subscribers {
		select {
		case ch <- event:
		default:
			// Channel is full, skip to prevent blocking
		}
	}
}

// ResetNodeState resets the failure detection state for a specific node.
// This can be used when a node recovers and should be monitored fresh.
func (d *Detector) ResetNodeState(nodeID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.nodeStates, nodeID)
}

// GetNodeState returns the current failure detection state for a node.
// Returns nil if the node is not being tracked.
func (d *Detector) GetNodeState(nodeID string) *nodeState {
	d.mu.Lock()
	defer d.mu.Unlock()

	if state, exists := d.nodeStates[nodeID]; exists {
		// Return a copy to prevent external modification
		copy := *state
		return &copy
	}
	return nil
}

// CheckNow triggers an immediate node health check.
// This is useful for testing or when you want to check immediately after a change.
func (d *Detector) CheckNow(ctx context.Context) {
	d.checkNodes(ctx)
}
