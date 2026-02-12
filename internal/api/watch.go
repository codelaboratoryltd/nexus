package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/codelaboratoryltd/nexus/internal/state"
)

// ConfigWatcher provides access to the change event hub.
type ConfigWatcher interface {
	WatchHub() *state.WatchHub
}

// SetConfigWatcher sets the config watcher for SSE streaming.
func (s *Server) SetConfigWatcher(watcher ConfigWatcher) {
	s.configWatcher = watcher
}

// watchHandler streams config changes via Server-Sent Events (SSE).
// Query parameters:
//   - prefix: filter by key prefix (e.g., "pools", "allocations", "nodes")
//
// Headers:
//   - Last-Event-ID: resume from this event ID (for reconnection catch-up)
func (s *Server) watchHandler(w http.ResponseWriter, r *http.Request) {
	if s.configWatcher == nil {
		respondError(w, http.StatusServiceUnavailable, "config watching not available (standalone mode)")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	prefix := r.URL.Query().Get("prefix")
	hub := s.configWatcher.WatchHub()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Handle reconnection: replay missed events
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		afterID, err := strconv.ParseUint(lastID, 10, 64)
		if err == nil {
			missed := hub.Replay(afterID, prefix)
			for _, event := range missed {
				if err := writeSSEEvent(w, event); err != nil {
					return
				}
			}
			flusher.Flush()
		}
	}

	// Subscribe to live events
	watcher := hub.Subscribe(prefix)
	defer hub.Unsubscribe(watcher)

	// Notify the client that the stream is ready
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case event, ok := <-watcher.C:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ssePayload is the JSON payload sent in each SSE event.
type ssePayload struct {
	Type      string `json:"type"`
	Key       string `json:"key"`
	Prefix    string `json:"prefix"`
	Timestamp string `json:"timestamp"`
	Value     []byte `json:"value,omitempty"`
}

// writeSSEEvent writes a single SSE event to the response writer.
func writeSSEEvent(w http.ResponseWriter, event state.ChangeEvent) error {
	payload := ssePayload{
		Type:      event.Type,
		Key:       event.Key,
		Prefix:    event.Prefix,
		Timestamp: event.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		Value:     event.Value,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", event.ID, data)
	return err
}
