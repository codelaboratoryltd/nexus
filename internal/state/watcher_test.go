package state

import (
	"testing"
	"time"
)

func TestExtractPrefix(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"/pools/pool1", "pools"},
		{"/allocations/pool1/sub1", "allocations"},
		{"/nodes/node1", "nodes"},
		{"pools", "pools"},
		{"/pools", "pools"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractPrefix(tt.key)
		if got != tt.want {
			t.Errorf("extractPrefix(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestWatchHub_SubscribeAndPublish(t *testing.T) {
	hub := NewWatchHub(100)

	w := hub.Subscribe("pools")
	defer hub.Unsubscribe(w)

	hub.Publish("put", "/pools/pool1", []byte("data"))

	select {
	case event := <-w.C:
		if event.Type != "put" {
			t.Errorf("event.Type = %q, want %q", event.Type, "put")
		}
		if event.Key != "/pools/pool1" {
			t.Errorf("event.Key = %q, want %q", event.Key, "/pools/pool1")
		}
		if event.Prefix != "pools" {
			t.Errorf("event.Prefix = %q, want %q", event.Prefix, "pools")
		}
		if event.ID != 1 {
			t.Errorf("event.ID = %d, want 1", event.ID)
		}
		if string(event.Value) != "data" {
			t.Errorf("event.Value = %q, want %q", event.Value, "data")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestWatchHub_PrefixFilter(t *testing.T) {
	hub := NewWatchHub(100)

	poolWatcher := hub.Subscribe("pools")
	defer hub.Unsubscribe(poolWatcher)

	// Publish an allocation event - should not reach the pool watcher
	hub.Publish("put", "/allocations/pool1/sub1", nil)

	select {
	case <-poolWatcher.C:
		t.Fatal("pool watcher should not receive allocation events")
	case <-time.After(50 * time.Millisecond):
		// Expected: no event
	}

	// Publish a pool event - should reach the watcher
	hub.Publish("put", "/pools/pool1", nil)

	select {
	case event := <-poolWatcher.C:
		if event.Prefix != "pools" {
			t.Errorf("event.Prefix = %q, want %q", event.Prefix, "pools")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pool event")
	}
}

func TestWatchHub_EmptyPrefixMatchesAll(t *testing.T) {
	hub := NewWatchHub(100)

	allWatcher := hub.Subscribe("")
	defer hub.Unsubscribe(allWatcher)

	hub.Publish("put", "/pools/pool1", nil)
	hub.Publish("put", "/allocations/pool1/sub1", nil)

	// Should receive both events
	for i := 0; i < 2; i++ {
		select {
		case <-allWatcher.C:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}
}

func TestWatchHub_DeleteEvent(t *testing.T) {
	hub := NewWatchHub(100)

	w := hub.Subscribe("")
	defer hub.Unsubscribe(w)

	hub.Publish("delete", "/pools/pool1", nil)

	select {
	case event := <-w.C:
		if event.Type != "delete" {
			t.Errorf("event.Type = %q, want %q", event.Type, "delete")
		}
		if event.Value != nil {
			t.Errorf("event.Value should be nil for deletes")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestWatchHub_Replay(t *testing.T) {
	hub := NewWatchHub(100)

	hub.Publish("put", "/pools/pool1", []byte("v1"))
	hub.Publish("put", "/pools/pool2", []byte("v2"))
	hub.Publish("put", "/allocations/pool1/sub1", nil)

	// Replay all events after ID 0
	events := hub.Replay(0, "")
	if len(events) != 3 {
		t.Fatalf("Replay(0, \"\") returned %d events, want 3", len(events))
	}

	// Replay only pool events after ID 0
	events = hub.Replay(0, "pools")
	if len(events) != 2 {
		t.Fatalf("Replay(0, \"pools\") returned %d events, want 2", len(events))
	}

	// Replay events after ID 1
	events = hub.Replay(1, "")
	if len(events) != 2 {
		t.Fatalf("Replay(1, \"\") returned %d events, want 2", len(events))
	}

	// Replay events after ID 3 (none)
	events = hub.Replay(3, "")
	if len(events) != 0 {
		t.Fatalf("Replay(3, \"\") returned %d events, want 0", len(events))
	}
}

func TestWatchHub_ReplayBufferLimit(t *testing.T) {
	hub := NewWatchHub(10)

	// Publish 15 events to overflow the buffer
	for i := 0; i < 15; i++ {
		hub.Publish("put", "/pools/pool", nil)
	}

	// Should have kept some events but not all 15
	events := hub.Replay(0, "")
	if len(events) > 10 {
		t.Errorf("replay buffer should be capped, got %d events", len(events))
	}
	if len(events) == 0 {
		t.Error("replay buffer should have some events")
	}
}

func TestWatchHub_WatcherCount(t *testing.T) {
	hub := NewWatchHub(100)

	if hub.WatcherCount() != 0 {
		t.Errorf("WatcherCount = %d, want 0", hub.WatcherCount())
	}

	w1 := hub.Subscribe("pools")
	w2 := hub.Subscribe("allocations")

	if hub.WatcherCount() != 2 {
		t.Errorf("WatcherCount = %d, want 2", hub.WatcherCount())
	}

	hub.Unsubscribe(w1)
	if hub.WatcherCount() != 1 {
		t.Errorf("WatcherCount = %d, want 1", hub.WatcherCount())
	}

	hub.Unsubscribe(w2)
	if hub.WatcherCount() != 0 {
		t.Errorf("WatcherCount = %d, want 0", hub.WatcherCount())
	}
}

func TestWatchHub_SequenceIncreases(t *testing.T) {
	hub := NewWatchHub(100)

	w := hub.Subscribe("")
	defer hub.Unsubscribe(w)

	hub.Publish("put", "/pools/a", nil)
	hub.Publish("put", "/pools/b", nil)

	e1 := <-w.C
	e2 := <-w.C

	if e2.ID <= e1.ID {
		t.Errorf("sequence should increase: %d should be > %d", e2.ID, e1.ID)
	}
}
