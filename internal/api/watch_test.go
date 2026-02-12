package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/state"
)

// mockConfigWatcher implements ConfigWatcher for testing.
type mockConfigWatcher struct {
	hub *state.WatchHub
}

func (m *mockConfigWatcher) WatchHub() *state.WatchHub {
	return m.hub
}

func TestWatchHandler_StandaloneMode(t *testing.T) {
	server, _, _, _ := setupTestServer()
	router := setupTestRouter(server)
	// configWatcher is nil (standalone mode)

	req := httptest.NewRequest("GET", "/api/v1/watch", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("watch handler in standalone mode returned status %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestWatchHandler_StreamsEvents(t *testing.T) {
	server, _, _, _ := setupTestServer()

	hub := state.NewWatchHub(100)
	server.SetConfigWatcher(&mockConfigWatcher{hub: hub})

	router := setupTestRouter(server)

	// Use a real HTTP test server for proper SSE streaming
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Start request with a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/watch?prefix=pools", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", contentType)
	}

	scanner := bufio.NewScanner(resp.Body)

	// Read the "connected" comment - this confirms the subscription is active
	readUntilLine(t, scanner, ": connected")

	// Small delay to ensure the watcher goroutine is ready
	time.Sleep(50 * time.Millisecond)

	// Publish an event
	hub.Publish("put", "/pools/pool1", []byte(`{"id":"pool1"}`))

	// Read SSE event
	var eventLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && len(eventLines) > 0 {
			break // End of SSE event
		}
		if line != "" {
			eventLines = append(eventLines, line)
		}
	}

	if len(eventLines) < 2 {
		t.Fatalf("Expected at least 2 event lines (id + event + data), got %d: %v", len(eventLines), eventLines)
	}

	// Check event has id and data
	hasID := false
	hasData := false
	for _, line := range eventLines {
		if strings.HasPrefix(line, "id: ") {
			hasID = true
		}
		if strings.HasPrefix(line, "data: ") {
			hasData = true
			// Verify the data contains expected fields
			data := strings.TrimPrefix(line, "data: ")
			if !strings.Contains(data, `"type":"put"`) {
				t.Errorf("data should contain type:put, got %s", data)
			}
			if !strings.Contains(data, `"key":"/pools/pool1"`) {
				t.Errorf("data should contain key, got %s", data)
			}
			if !strings.Contains(data, `"prefix":"pools"`) {
				t.Errorf("data should contain prefix:pools, got %s", data)
			}
		}
	}

	if !hasID {
		t.Error("SSE event missing id field")
	}
	if !hasData {
		t.Error("SSE event missing data field")
	}
}

func TestWatchHandler_PrefixFiltering(t *testing.T) {
	server, _, _, _ := setupTestServer()

	hub := state.NewWatchHub(100)
	server.SetConfigWatcher(&mockConfigWatcher{hub: hub})

	router := setupTestRouter(server)
	ts := httptest.NewServer(router)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Subscribe to pools only
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/watch?prefix=pools", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	readUntilLine(t, scanner, ": connected")

	// Publish an allocation event (should be filtered out)
	hub.Publish("put", "/allocations/pool1/sub1", nil)

	// Publish a pool event (should come through)
	hub.Publish("put", "/pools/pool1", nil)

	// Read the event - should be the pool event, not the allocation
	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLine = line
		}
		if line == "" && dataLine != "" {
			break
		}
	}

	if !strings.Contains(dataLine, `"prefix":"pools"`) {
		t.Errorf("Expected pool event, got: %s", dataLine)
	}
}

func TestWatchHandler_ReconnectionReplay(t *testing.T) {
	server, _, _, _ := setupTestServer()

	hub := state.NewWatchHub(100)
	server.SetConfigWatcher(&mockConfigWatcher{hub: hub})

	// Publish events before connecting (to be replayed)
	hub.Publish("put", "/pools/pool1", []byte("v1"))
	hub.Publish("put", "/pools/pool2", []byte("v2"))

	router := setupTestRouter(server)
	ts := httptest.NewServer(router)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Connect with Last-Event-ID: 1 (should replay event 2 only)
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/watch?prefix=pools", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// Read replayed event (ID 2)
	var eventCount int
	var lastID string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			lastID = strings.TrimPrefix(line, "id: ")
			eventCount++
		}
		if line == ": connected" {
			break
		}
	}

	if eventCount != 1 {
		t.Errorf("Expected 1 replayed event, got %d", eventCount)
	}
	if lastID != "2" {
		t.Errorf("Expected replayed event ID 2, got %s", lastID)
	}
}

// readUntilLine reads lines from the scanner until it finds the target line.
func readUntilLine(t *testing.T, scanner *bufio.Scanner, target string) {
	t.Helper()
	for scanner.Scan() {
		if scanner.Text() == target {
			return
		}
	}
	t.Fatalf("Did not find line %q in stream", target)
}
