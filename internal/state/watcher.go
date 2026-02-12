package state

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ChangeEvent represents a config change that occurred in the CRDT.
type ChangeEvent struct {
	// ID is a monotonically increasing sequence number for reconnection support.
	ID uint64 `json:"id"`
	// Type is the kind of change: "put" or "delete".
	Type string `json:"type"`
	// Key is the full CRDT key path (e.g., "/pools/pool1").
	Key string `json:"key"`
	// Prefix is the top-level key category (e.g., "pools", "allocations", "nodes").
	Prefix string `json:"prefix"`
	// Timestamp is when the change was observed.
	Timestamp time.Time `json:"timestamp"`
	// Value is the raw value bytes (nil for deletions).
	Value []byte `json:"value,omitempty"`
}

// Watcher receives change events filtered by prefix.
type Watcher struct {
	C      chan ChangeEvent
	prefix string
}

// WatchHub manages change event broadcasting to watchers.
type WatchHub struct {
	mu          sync.RWMutex
	watchers    map[*Watcher]struct{}
	seq         atomic.Uint64
	replayMu    sync.RWMutex
	replayBuf   []ChangeEvent
	replayLimit int
}

// NewWatchHub creates a new WatchHub with a replay buffer for reconnection catch-up.
func NewWatchHub(replayLimit int) *WatchHub {
	if replayLimit <= 0 {
		replayLimit = 1000
	}
	return &WatchHub{
		watchers:    make(map[*Watcher]struct{}),
		replayBuf:   make([]ChangeEvent, 0, replayLimit),
		replayLimit: replayLimit,
	}
}

// Subscribe creates a new watcher that receives events matching the given prefix.
// An empty prefix matches all events.
func (h *WatchHub) Subscribe(prefix string) *Watcher {
	w := &Watcher{
		C:      make(chan ChangeEvent, 64),
		prefix: prefix,
	}
	h.mu.Lock()
	h.watchers[w] = struct{}{}
	h.mu.Unlock()
	return w
}

// Unsubscribe removes a watcher and closes its channel.
func (h *WatchHub) Unsubscribe(w *Watcher) {
	h.mu.Lock()
	delete(h.watchers, w)
	h.mu.Unlock()
	close(w.C)
}

// Publish broadcasts a change event to all matching watchers.
func (h *WatchHub) Publish(changeType, key string, value []byte) {
	prefix := extractPrefix(key)
	event := ChangeEvent{
		ID:        h.seq.Add(1),
		Type:      changeType,
		Key:       key,
		Prefix:    prefix,
		Timestamp: time.Now(),
		Value:     value,
	}

	// Store in replay buffer
	h.replayMu.Lock()
	if len(h.replayBuf) >= h.replayLimit {
		// Drop oldest half when full
		copy(h.replayBuf, h.replayBuf[h.replayLimit/2:])
		h.replayBuf = h.replayBuf[:h.replayLimit/2]
	}
	h.replayBuf = append(h.replayBuf, event)
	h.replayMu.Unlock()

	// Broadcast to watchers
	h.mu.RLock()
	for w := range h.watchers {
		if w.prefix == "" || w.prefix == prefix {
			select {
			case w.C <- event:
			default:
				// Drop event if watcher is slow
			}
		}
	}
	h.mu.RUnlock()
}

// Replay returns events with ID > afterID that match the given prefix.
func (h *WatchHub) Replay(afterID uint64, prefix string) []ChangeEvent {
	h.replayMu.RLock()
	defer h.replayMu.RUnlock()

	var events []ChangeEvent
	for _, e := range h.replayBuf {
		if e.ID > afterID && (prefix == "" || prefix == e.Prefix) {
			events = append(events, e)
		}
	}
	return events
}

// WatcherCount returns the number of active watchers.
func (h *WatchHub) WatcherCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.watchers)
}

// extractPrefix returns the first path component from a CRDT key.
// For example, "/pools/pool1" returns "pools".
func extractPrefix(key string) string {
	key = strings.TrimPrefix(key, "/")
	if idx := strings.IndexByte(key, '/'); idx > 0 {
		return key[:idx]
	}
	return key
}
