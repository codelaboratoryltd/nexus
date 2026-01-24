// Package reassignment handles automatic shard/partition reassignment when nodes fail.
// It subscribes to failure detector events and redistributes failed node's partitions
// to surviving nodes in the hashring.
package reassignment

import (
	"context"
	"sync"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/failure"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	logging "github.com/ipfs/go-log/v2"
	"github.com/prometheus/client_golang/prometheus"
)

var log = logging.Logger("nexus-reassignment")

// Metrics holds Prometheus metrics for the reassigner.
type Metrics struct {
	// ReassignmentsTotal is a counter of total reassignment operations.
	ReassignmentsTotal prometheus.Counter

	// ReassignmentLatency measures how long each reassignment takes.
	ReassignmentLatency prometheus.Histogram

	// PartitionsMoved is a counter of total partitions moved during reassignments.
	PartitionsMoved prometheus.Counter

	// ActiveNodes is a gauge of currently active nodes in the ring.
	ActiveNodes prometheus.Gauge
}

// NewMetrics creates and registers Prometheus metrics for the reassigner.
func NewMetrics(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		ReassignmentsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "nexus",
			Subsystem: "reassignment",
			Name:      "total",
			Help:      "Total number of shard reassignment operations.",
		}),
		ReassignmentLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "nexus",
			Subsystem: "reassignment",
			Name:      "latency_seconds",
			Help:      "Time taken to complete a reassignment operation.",
			Buckets:   prometheus.DefBuckets,
		}),
		PartitionsMoved: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "nexus",
			Subsystem: "reassignment",
			Name:      "partitions_moved_total",
			Help:      "Total number of partitions moved during reassignments.",
		}),
		ActiveNodes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "nexus",
			Subsystem: "reassignment",
			Name:      "active_nodes",
			Help:      "Number of active nodes in the hashring.",
		}),
	}

	if registerer != nil {
		registerer.MustRegister(
			m.ReassignmentsTotal,
			m.ReassignmentLatency,
			m.PartitionsMoved,
			m.ActiveNodes,
		)
	}

	return m
}

// ReassignmentEvent represents a shard reassignment that occurred.
type ReassignmentEvent struct {
	FailedNodeID string
	Timestamp    time.Time
	// PartitionsMoved maps pool ID to the list of virtual nodes that were reassigned.
	PartitionsMoved map[hashring.PoolID][]PartitionReassignment
	Duration        time.Duration
}

// PartitionReassignment represents a single partition that was reassigned.
type PartitionReassignment struct {
	VirtualNodeID hashring.VirtualNodeID
	OldOwner      hashring.NodeID
	NewOwner      hashring.NodeID
}

// Config holds configuration for the Reassigner.
type Config struct {
	// Hashring is the virtual hashring to manage.
	Hashring *hashring.VirtualHashRing

	// Detector is the failure detector to subscribe to.
	Detector *failure.Detector

	// Metrics is optional Prometheus metrics.
	Metrics *Metrics

	// OnReassignment is an optional callback when reassignment occurs.
	OnReassignment func(event ReassignmentEvent)
}

// Reassigner handles shard reassignment when nodes fail.
type Reassigner struct {
	mu sync.Mutex

	hashring *hashring.VirtualHashRing
	detector *failure.Detector
	metrics  *Metrics

	// onReassignment is called after each reassignment.
	onReassignment func(event ReassignmentEvent)

	// subscribers receive reassignment events.
	subscribers []chan<- ReassignmentEvent

	// failureEvents is the channel for receiving failure events from the detector.
	failureEvents chan failure.NodeFailedEvent

	// running indicates whether the reassigner is currently running.
	running bool

	// stopCh is used to signal the reassigner to stop.
	stopCh chan struct{}
}

// NewReassigner creates a new Reassigner with the given configuration.
func NewReassigner(cfg Config) *Reassigner {
	r := &Reassigner{
		hashring:       cfg.Hashring,
		detector:       cfg.Detector,
		metrics:        cfg.Metrics,
		onReassignment: cfg.OnReassignment,
		failureEvents:  make(chan failure.NodeFailedEvent, 100),
		stopCh:         make(chan struct{}),
	}

	return r
}

// Subscribe adds a channel to receive reassignment events.
// The channel should be buffered to prevent blocking the reassigner.
func (r *Reassigner) Subscribe(ch chan<- ReassignmentEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscribers = append(r.subscribers, ch)
}

// Unsubscribe removes a channel from receiving reassignment events.
func (r *Reassigner) Unsubscribe(ch chan<- ReassignmentEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, sub := range r.subscribers {
		if sub == ch {
			r.subscribers = append(r.subscribers[:i], r.subscribers[i+1:]...)
			return
		}
	}
}

// Start begins listening for failure events and handling reassignments.
// It returns immediately and runs the reassigner in a background goroutine.
func (r *Reassigner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.stopCh = make(chan struct{})

	// Subscribe to failure detector events
	r.detector.Subscribe(r.failureEvents)
	r.mu.Unlock()

	go r.run(ctx)
	return nil
}

// Stop stops the reassigner.
func (r *Reassigner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false

	// Unsubscribe from failure detector
	r.detector.Unsubscribe(r.failureEvents)

	close(r.stopCh)
	r.mu.Unlock()
}

// IsRunning returns whether the reassigner is currently running.
func (r *Reassigner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// run is the main event loop.
func (r *Reassigner) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
			return
		case <-r.stopCh:
			return
		case event := <-r.failureEvents:
			r.handleNodeFailure(event)
		}
	}
}

// handleNodeFailure processes a node failure event and reassigns its shards.
func (r *Reassigner) handleNodeFailure(event failure.NodeFailedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	start := time.Now()
	nodeID := hashring.NodeID(event.NodeID)

	log.Infof("Handling node failure for %s (expired at %v, detected at %v)",
		event.NodeID, event.BestBefore, event.DetectedAt)

	// Get the mapping before removal to track what was reassigned
	mappingBefore := r.hashring.GetHashMapping()

	// Get partitions owned by the failed node before removal
	ownedPartitions := mappingBefore.ListOwnedVirtualHashes(nodeID)
	partitionsCount := 0
	for _, vnodes := range ownedPartitions {
		partitionsCount += len(vnodes)
	}

	// Remove the node from the ring - this triggers automatic redistribution
	if err := r.hashring.RemoveNode(nodeID); err != nil {
		log.Errorf("Failed to remove node %s from hashring: %v", event.NodeID, err)
		return
	}

	// Get the mapping after removal to determine new owners
	mappingAfter := r.hashring.GetHashMapping()

	// Build the reassignment details
	reassignments := make(map[hashring.PoolID][]PartitionReassignment)

	for poolID, vnodes := range ownedPartitions {
		for vnodeID := range vnodes {
			// Find the new owner
			var newOwner hashring.NodeID
			if mappingAfter.PoolVNodeNode[poolID] != nil {
				newOwner = mappingAfter.PoolVNodeNode[poolID][vnodeID]
			}

			reassignment := PartitionReassignment{
				VirtualNodeID: vnodeID,
				OldOwner:      nodeID,
				NewOwner:      newOwner,
			}
			reassignments[poolID] = append(reassignments[poolID], reassignment)

			log.Infof("Partition %s (pool %s) reassigned from %s to %s",
				vnodeID, poolID, nodeID, newOwner)
		}
	}

	duration := time.Since(start)

	// Create reassignment event
	reassignmentEvent := ReassignmentEvent{
		FailedNodeID:    event.NodeID,
		Timestamp:       start,
		PartitionsMoved: reassignments,
		Duration:        duration,
	}

	// Update metrics
	if r.metrics != nil {
		r.metrics.ReassignmentsTotal.Inc()
		r.metrics.ReassignmentLatency.Observe(duration.Seconds())
		r.metrics.PartitionsMoved.Add(float64(partitionsCount))
		r.metrics.ActiveNodes.Set(float64(len(r.hashring.ListNodes())))
	}

	log.Infof("Reassignment complete for node %s: %d partitions moved in %v",
		event.NodeID, partitionsCount, duration)

	// Call callback if set
	if r.onReassignment != nil {
		// Release lock for callback to prevent deadlocks
		r.mu.Unlock()
		r.onReassignment(reassignmentEvent)
		r.mu.Lock()
	}

	// Notify subscribers (non-blocking)
	for _, ch := range r.subscribers {
		select {
		case ch <- reassignmentEvent:
		default:
			// Channel is full, skip to prevent blocking
			log.Warn("Subscriber channel full, dropping reassignment event")
		}
	}
}

// HandleNodeFailureNow immediately handles a node failure.
// This is useful for testing or manual intervention.
func (r *Reassigner) HandleNodeFailureNow(nodeID string) {
	event := failure.NodeFailedEvent{
		NodeID:       nodeID,
		DetectedAt:   time.Now(),
		BestBefore:   time.Now().Add(-time.Minute),
		TimeSinceDue: time.Minute,
	}
	r.handleNodeFailure(event)
}

// GetActiveNodeCount returns the number of active nodes in the hashring.
func (r *Reassigner) GetActiveNodeCount() int {
	return len(r.hashring.ListNodes())
}
