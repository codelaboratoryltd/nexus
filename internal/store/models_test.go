package store

import (
	"math/big"
	"net"
	"testing"
	"time"
)

func TestPool_Validate(t *testing.T) {
	tests := []struct {
		name    string
		pool    Pool
		wantErr bool
	}{
		{
			name: "valid pool",
			pool: Pool{
				ID:     "pool1",
				CIDR:   *mustParseCIDR("192.168.0.0/24"),
				Prefix: 28,
			},
			wantErr: false,
		},
		{
			name: "valid pool with metadata",
			pool: Pool{
				ID:       "pool2",
				CIDR:     *mustParseCIDR("10.0.0.0/16"),
				Prefix:   24,
				Metadata: map[string]string{"env": "prod"},
			},
			wantErr: false,
		},
		{
			name: "valid pool with exclusions",
			pool: Pool{
				ID:         "pool3",
				CIDR:       *mustParseCIDR("172.16.0.0/16"),
				Prefix:     24,
				Exclusions: []string{"172.16.0.0/24", "172.16.1.0/24"},
			},
			wantErr: false,
		},
		{
			name: "nil IP in CIDR",
			pool: Pool{
				ID:     "pool4",
				CIDR:   net.IPNet{IP: nil, Mask: net.CIDRMask(24, 32)},
				Prefix: 28,
			},
			wantErr: true,
		},
		{
			name: "nil mask in CIDR",
			pool: Pool{
				ID:     "pool5",
				CIDR:   net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: nil},
				Prefix: 28,
			},
			wantErr: true,
		},
		{
			name: "empty mask in CIDR",
			pool: Pool{
				ID:     "pool6",
				CIDR:   net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: net.IPMask{}},
				Prefix: 28,
			},
			wantErr: true,
		},
		{
			name: "prefix smaller than CIDR mask",
			pool: Pool{
				ID:     "pool7",
				CIDR:   *mustParseCIDR("192.168.0.0/24"),
				Prefix: 16, // Smaller than /24
			},
			wantErr: true,
		},
		{
			name: "prefix larger than max bits",
			pool: Pool{
				ID:     "pool8",
				CIDR:   *mustParseCIDR("192.168.0.0/24"),
				Prefix: 33, // Larger than 32 for IPv4
			},
			wantErr: true,
		},
		{
			name: "prefix equal to CIDR mask",
			pool: Pool{
				ID:     "pool9",
				CIDR:   *mustParseCIDR("192.168.0.0/24"),
				Prefix: 24,
			},
			wantErr: false,
		},
		{
			name: "prefix at max (32)",
			pool: Pool{
				ID:     "pool10",
				CIDR:   *mustParseCIDR("192.168.0.0/24"),
				Prefix: 32,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.pool.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Pool.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAllocationKey_GetAllocation(t *testing.T) {
	pool := &Pool{
		ID:     "pool1",
		CIDR:   *mustParseCIDR("192.168.0.0/24"),
		Prefix: 28,
	}

	tests := []struct {
		name       string
		key        AllocationKey
		keysValue  []byte
		wantIP     string
		wantPoolID string
		wantSubID  string
		wantErr    bool
	}{
		{
			name: "valid allocation at offset 1",
			key: AllocationKey{
				PoolID:       "pool1",
				IPOffset:     big.NewInt(1),
				SubscriberID: "sub1",
			},
			keysValue:  nil,
			wantIP:     "192.168.0.0",
			wantPoolID: "pool1",
			wantSubID:  "sub1",
			wantErr:    false,
		},
		{
			name: "valid allocation at offset 101",
			key: AllocationKey{
				PoolID:       "pool1",
				IPOffset:     big.NewInt(101),
				SubscriberID: "sub2",
			},
			keysValue:  nil,
			wantIP:     "192.168.0.100",
			wantPoolID: "pool1",
			wantSubID:  "sub2",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alloc, err := tt.key.GetAllocation(pool, tt.keysValue)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAllocation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if alloc.PoolID != tt.wantPoolID {
				t.Errorf("PoolID = %s, want %s", alloc.PoolID, tt.wantPoolID)
			}
			if alloc.SubscriberID != tt.wantSubID {
				t.Errorf("SubscriberID = %s, want %s", alloc.SubscriberID, tt.wantSubID)
			}
			if alloc.IP.String() != tt.wantIP {
				t.Errorf("IP = %s, want %s", alloc.IP.String(), tt.wantIP)
			}
		})
	}
}

func TestAllocationKey_GetAllocation_WithTimestamp(t *testing.T) {
	pool := &Pool{
		ID:     "pool1",
		CIDR:   *mustParseCIDR("192.168.0.0/24"),
		Prefix: 28,
	}

	key := AllocationKey{
		PoolID:       "pool1",
		IPOffset:     big.NewInt(1),
		SubscriberID: "sub1",
	}

	// Create timestamp bytes (big-endian uint64)
	now := time.Now()
	timestampBytes := make([]byte, 8)
	nanos := uint64(now.UnixNano())
	for i := 7; i >= 0; i-- {
		timestampBytes[i] = byte(nanos)
		nanos >>= 8
	}

	alloc, err := key.GetAllocation(pool, timestampBytes)
	if err != nil {
		t.Fatalf("GetAllocation() error = %v", err)
	}

	if alloc.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
}

func TestNode_Fields(t *testing.T) {
	bestBefore := time.Now().Add(time.Hour)
	node := Node{
		ID:         "node1",
		BestBefore: bestBefore,
		Metadata:   map[string]string{"role": "write"},
	}

	if node.ID != "node1" {
		t.Errorf("Node.ID = %s, want node1", node.ID)
	}
	if !node.BestBefore.Equal(bestBefore) {
		t.Errorf("Node.BestBefore = %v, want %v", node.BestBefore, bestBefore)
	}
	if node.Metadata["role"] != "write" {
		t.Errorf("Node.Metadata[role] = %s, want write", node.Metadata["role"])
	}
}

func TestAllocation_Fields(t *testing.T) {
	timestamp := time.Now()
	ip := net.ParseIP("192.168.0.1")
	alloc := Allocation{
		PoolID:       "pool1",
		IP:           ip,
		Timestamp:    timestamp,
		SubscriberID: "sub1",
	}

	if alloc.PoolID != "pool1" {
		t.Errorf("Allocation.PoolID = %s, want pool1", alloc.PoolID)
	}
	if !alloc.IP.Equal(ip) {
		t.Errorf("Allocation.IP = %v, want %v", alloc.IP, ip)
	}
	if !alloc.Timestamp.Equal(timestamp) {
		t.Errorf("Allocation.Timestamp = %v, want %v", alloc.Timestamp, timestamp)
	}
	if alloc.SubscriberID != "sub1" {
		t.Errorf("Allocation.SubscriberID = %s, want sub1", alloc.SubscriberID)
	}
}

func TestAllocationKey_Fields(t *testing.T) {
	key := AllocationKey{
		PoolID:       "pool1",
		IPOffset:     big.NewInt(100),
		SubscriberID: "sub1",
	}

	if key.PoolID != "pool1" {
		t.Errorf("AllocationKey.PoolID = %s, want pool1", key.PoolID)
	}
	if key.IPOffset.Int64() != 100 {
		t.Errorf("AllocationKey.IPOffset = %d, want 100", key.IPOffset.Int64())
	}
	if key.SubscriberID != "sub1" {
		t.Errorf("AllocationKey.SubscriberID = %s, want sub1", key.SubscriberID)
	}
}

func TestPoolCIDR_IPv6(t *testing.T) {
	pool := Pool{
		ID:     "ipv6-pool",
		CIDR:   *mustParseCIDR("2001:db8::/32"),
		Prefix: 64,
	}

	err := pool.Validate()
	if err != nil {
		t.Errorf("IPv6 pool validation failed: %v", err)
	}
}

func TestStoreErrors(t *testing.T) {
	// Test that all error variables are non-nil and have messages
	errors := []error{
		ErrInvalidPoolPrefix,
		ErrInvalidPoolCIDR,
		ErrMalformedKey,
		ErrNoAllocationFound,
		ErrPoolNotFound,
	}

	for _, err := range errors {
		if err == nil {
			t.Error("Error variable is nil")
			continue
		}
		if err.Error() == "" {
			t.Error("Error message is empty")
		}
	}
}

func TestErrInvalidPoolPrefix(t *testing.T) {
	if ErrInvalidPoolPrefix.Error() != "invalid pool prefix" {
		t.Errorf("ErrInvalidPoolPrefix = %q", ErrInvalidPoolPrefix.Error())
	}
}

func TestErrInvalidPoolCIDR(t *testing.T) {
	if ErrInvalidPoolCIDR.Error() != "invalid pool CIDR" {
		t.Errorf("ErrInvalidPoolCIDR = %q", ErrInvalidPoolCIDR.Error())
	}
}

func TestErrMalformedKey(t *testing.T) {
	if ErrMalformedKey.Error() != "malformed key" {
		t.Errorf("ErrMalformedKey = %q", ErrMalformedKey.Error())
	}
}

func TestErrNoAllocationFound(t *testing.T) {
	if ErrNoAllocationFound.Error() != "no allocation found" {
		t.Errorf("ErrNoAllocationFound = %q", ErrNoAllocationFound.Error())
	}
}

func TestErrPoolNotFound(t *testing.T) {
	if ErrPoolNotFound.Error() != "pool not found" {
		t.Errorf("ErrPoolNotFound = %q", ErrPoolNotFound.Error())
	}
}

// Helper function
func mustParseCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return network
}
