package state

import (
	"testing"
	"time"
)

// TestNodeInfoStruct tests the nodeInfo struct behavior
func TestNodeInfoStruct(t *testing.T) {
	tests := []struct {
		name       string
		bestBefore time.Time
		isWrite    bool
	}{
		{
			name:       "write node",
			bestBefore: time.Now().Add(time.Hour),
			isWrite:    true,
		},
		{
			name:       "read node",
			bestBefore: time.Now().Add(time.Hour),
			isWrite:    false,
		},
		{
			name:       "expired write node",
			bestBefore: time.Now().Add(-time.Hour),
			isWrite:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := nodeInfo{
				bestBefore: tt.bestBefore,
				isWrite:    tt.isWrite,
			}

			if info.bestBefore != tt.bestBefore {
				t.Errorf("nodeInfo bestBefore = %v, want %v", info.bestBefore, tt.bestBefore)
			}
			if info.isWrite != tt.isWrite {
				t.Errorf("nodeInfo isWrite = %v, want %v", info.isWrite, tt.isWrite)
			}
		})
	}
}

// TestConfigDefaults tests default configuration values
func TestConfigDefaults(t *testing.T) {
	// Test that default constants are defined correctly
	if DefaultEventQueueSize != 1000 {
		t.Errorf("DefaultEventQueueSize = %d, want 1000", DefaultEventQueueSize)
	}

	if DefaultRebroadcastInterval != 5*time.Second {
		t.Errorf("DefaultRebroadcastInterval = %v, want %v", DefaultRebroadcastInterval, 5*time.Second)
	}

	if OurName != "Nexus" {
		t.Errorf("OurName = %s, want Nexus", OurName)
	}
}

// TestErrorDefinitions tests that error types are properly defined
func TestErrorDefinitions(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "ErrPoolAlreadyExists",
			err:      ErrPoolAlreadyExists,
			expected: "pool already exists",
		},
		{
			name:     "ErrPoolNotFound",
			err:      ErrPoolNotFound,
			expected: "pool not found",
		},
		{
			name:     "ErrReadOnlyNode",
			err:      ErrReadOnlyNode,
			expected: "operation not allowed on read-only node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("%s error = %q, want %q", tt.name, tt.err.Error(), tt.expected)
			}
		})
	}
}

// TestGracefulShutdownOptions tests the GracefulShutdownOptions struct
func TestGracefulShutdownOptions(t *testing.T) {
	opts := &GracefulShutdownOptions{
		MaxWaitTime:     30 * time.Second,
		GracePeriod:     10 * time.Second,
		CheckInterval:   time.Second,
		RequiredMatches: 2,
		PeerSelector: func(metadata map[string]string) bool {
			return metadata["role"] == "write"
		},
	}

	if opts.MaxWaitTime != 30*time.Second {
		t.Errorf("MaxWaitTime = %v, want %v", opts.MaxWaitTime, 30*time.Second)
	}
	if opts.GracePeriod != 10*time.Second {
		t.Errorf("GracePeriod = %v, want %v", opts.GracePeriod, 10*time.Second)
	}
	if opts.CheckInterval != time.Second {
		t.Errorf("CheckInterval = %v, want %v", opts.CheckInterval, time.Second)
	}
	if opts.RequiredMatches != 2 {
		t.Errorf("RequiredMatches = %d, want 2", opts.RequiredMatches)
	}

	// Test peer selector
	writeNode := map[string]string{"role": "write"}
	readNode := map[string]string{"role": "read"}

	if !opts.PeerSelector(writeNode) {
		t.Error("PeerSelector should return true for write node")
	}
	if opts.PeerSelector(readNode) {
		t.Error("PeerSelector should return false for read node")
	}
}

// TestConfigStruct tests the Config struct
func TestConfigStruct(t *testing.T) {
	cfg := Config{
		ListenPort:     9000,
		Role:           "write",
		Topic:          "nexus-test",
		Bootstrap:      []string{"/ip4/127.0.0.1/tcp/4001/p2p/QmPeerID"},
		EventQueueSize: 2000,
	}

	if cfg.ListenPort != 9000 {
		t.Errorf("Config ListenPort = %d, want 9000", cfg.ListenPort)
	}
	if cfg.Role != "write" {
		t.Errorf("Config Role = %s, want write", cfg.Role)
	}
	if cfg.Topic != "nexus-test" {
		t.Errorf("Config Topic = %s, want nexus-test", cfg.Topic)
	}
	if len(cfg.Bootstrap) != 1 {
		t.Errorf("Config Bootstrap length = %d, want 1", len(cfg.Bootstrap))
	}
	if cfg.EventQueueSize != 2000 {
		t.Errorf("Config EventQueueSize = %d, want 2000", cfg.EventQueueSize)
	}
}

// TestConfigStructDefaults tests Config struct with zero values
func TestConfigStructDefaults(t *testing.T) {
	cfg := Config{}

	if cfg.ListenPort != 0 {
		t.Errorf("Config ListenPort default = %d, want 0", cfg.ListenPort)
	}
	if cfg.Role != "" {
		t.Errorf("Config Role default = %s, want empty", cfg.Role)
	}
	if cfg.Topic != "" {
		t.Errorf("Config Topic default = %s, want empty", cfg.Topic)
	}
	if cfg.Bootstrap != nil {
		t.Errorf("Config Bootstrap default = %v, want nil", cfg.Bootstrap)
	}
	if cfg.EventQueueSize != 0 {
		t.Errorf("Config EventQueueSize default = %d, want 0", cfg.EventQueueSize)
	}
}

// TestConfigRoleValues tests different role values
func TestConfigRoleValues(t *testing.T) {
	validRoles := []string{"core", "write", "read", "CORE", "WRITE", "READ"}

	for _, role := range validRoles {
		cfg := Config{Role: role}
		if cfg.Role != role {
			t.Errorf("Config Role = %s, want %s", cfg.Role, role)
		}
	}
}

// TestGracefulShutdownOptionsNilPeerSelector tests nil peer selector
func TestGracefulShutdownOptionsNilPeerSelector(t *testing.T) {
	opts := &GracefulShutdownOptions{
		MaxWaitTime:     30 * time.Second,
		GracePeriod:     10 * time.Second,
		CheckInterval:   time.Second,
		RequiredMatches: 1,
		PeerSelector:    nil,
	}

	if opts.PeerSelector != nil {
		t.Error("PeerSelector should be nil")
	}
}
