package hashring

import (
	"errors"
	"net"
	"sync"
	"testing"
)

func TestNewHashRing(t *testing.T) {
	hr := NewHashRing()
	if hr == nil {
		t.Fatal("NewHashRing returned nil")
	}
	if hr.hashIDs == nil {
		t.Error("hashIDs map is nil")
	}
	if hr.ipPools == nil {
		t.Error("ipPools map is nil")
	}
	if hr.subnets == nil {
		t.Error("subnets map is nil")
	}
	if hr.hashOrder == nil {
		t.Error("hashOrder map is nil")
	}
}

func TestHashRing_RegisterPool(t *testing.T) {
	tests := []struct {
		name    string
		pool    IPPool
		wantErr bool
	}{
		{
			name: "valid pool registration",
			pool: IPPool{
				ID:      "pool1",
				Network: mustParseCIDR("192.168.0.0/24"),
			},
			wantErr: false,
		},
		{
			name: "empty network IP",
			pool: IPPool{
				ID:      "pool2",
				Network: &net.IPNet{IP: nil, Mask: net.CIDRMask(24, 32)},
			},
			wantErr: true,
		},
		{
			name: "empty network mask",
			pool: IPPool{
				ID:      "pool3",
				Network: &net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: nil},
			},
			wantErr: true,
		},
		{
			name: "empty mask slice",
			pool: IPPool{
				ID:      "pool4",
				Network: &net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: net.IPMask{}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr := NewHashRing()
			err := hr.RegisterPool(tt.pool)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterPool() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHashRing_UnregisterPool(t *testing.T) {
	hr := NewHashRing()

	// Register a pool
	pool := IPPool{
		ID:      "pool1",
		Network: mustParseCIDR("192.168.0.0/24"),
	}
	if err := hr.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Verify pool was registered
	pools := hr.GetIPPools()
	if len(pools) != 1 {
		t.Errorf("Expected 1 pool, got %d", len(pools))
	}

	// Unregister the pool
	if err := hr.UnregisterPool("pool1"); err != nil {
		t.Errorf("UnregisterPool() error = %v", err)
	}

	// Verify pool was removed
	pools = hr.GetIPPools()
	if len(pools) != 0 {
		t.Errorf("Expected 0 pools after unregistration, got %d", len(pools))
	}
}

func TestHashRing_AddHash(t *testing.T) {
	hr := NewHashRing()

	// Add a hash
	if err := hr.AddHash("hash1"); err != nil {
		t.Errorf("AddHash() error = %v", err)
	}

	// Verify hash was added
	hashIDs := hr.GetHashIDs()
	if _, exists := hashIDs["hash1"]; !exists {
		t.Error("Hash was not added to hashIDs")
	}

	// Add the same hash again (should be idempotent)
	if err := hr.AddHash("hash1"); err != nil {
		t.Errorf("AddHash() for existing hash error = %v", err)
	}
}

func TestHashRing_RemoveHash(t *testing.T) {
	hr := NewHashRing()

	// Add a hash
	if err := hr.AddHash("hash1"); err != nil {
		t.Fatalf("Failed to add hash: %v", err)
	}

	// Remove the hash
	if err := hr.RemoveHash("hash1"); err != nil {
		t.Errorf("RemoveHash() error = %v", err)
	}

	// Verify hash was removed
	hashIDs := hr.GetHashIDs()
	if _, exists := hashIDs["hash1"]; exists {
		t.Error("Hash was not removed from hashIDs")
	}

	// Remove non-existent hash (should be idempotent)
	if err := hr.RemoveHash("nonexistent"); err != nil {
		t.Errorf("RemoveHash() for non-existent hash error = %v", err)
	}
}

func TestHashRing_GetHashIPNets(t *testing.T) {
	hr := NewHashRing()

	// Add hash and pool
	if err := hr.AddHash("hash1"); err != nil {
		t.Fatalf("Failed to add hash: %v", err)
	}
	pool := IPPool{
		ID:      "pool1",
		Network: mustParseCIDR("192.168.0.0/24"),
	}
	if err := hr.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Get subnets for the hash
	subnets, err := hr.GetHashIPNets("hash1")
	if err != nil {
		t.Errorf("GetHashIPNets() error = %v", err)
	}
	if subnets == nil {
		t.Error("GetHashIPNets() returned nil")
	}
	if _, exists := subnets["pool1"]; !exists {
		t.Error("Subnet for pool1 not found")
	}

	// Get subnets for non-existent hash
	_, err = hr.GetHashIPNets("nonexistent")
	if err == nil {
		t.Error("GetHashIPNets() should return error for non-existent hash")
	}
	var hashNotFoundErr *HashNotFoundError
	if !errors.As(err, &hashNotFoundErr) {
		t.Errorf("Expected HashNotFoundError, got %T", err)
	}
}

func TestHashRing_GetHashOrder(t *testing.T) {
	hr := NewHashRing()

	// Add multiple hashes
	hashes := []HashID{"hash1", "hash2", "hash3"}
	for _, h := range hashes {
		if err := hr.AddHash(h); err != nil {
			t.Fatalf("Failed to add hash: %v", err)
		}
	}

	// Get order for each hash
	for _, h := range hashes {
		order, err := hr.GetHashOrder(h)
		if err != nil {
			t.Errorf("GetHashOrder(%s) error = %v", h, err)
		}
		if order < 0 || order >= len(hashes) {
			t.Errorf("GetHashOrder(%s) returned invalid order %d", h, order)
		}
	}

	// Get order for non-existent hash
	_, err := hr.GetHashOrder("nonexistent")
	if err == nil {
		t.Error("GetHashOrder() should return error for non-existent hash")
	}
}

func TestHashRing_GetHashOrders(t *testing.T) {
	hr := NewHashRing()

	// Add multiple hashes
	hashes := []HashID{"hash1", "hash2", "hash3"}
	for _, h := range hashes {
		if err := hr.AddHash(h); err != nil {
			t.Fatalf("Failed to add hash: %v", err)
		}
	}

	// Get all orders
	orders := hr.GetHashOrders()
	if len(orders) != len(hashes) {
		t.Errorf("Expected %d orders, got %d", len(hashes), len(orders))
	}

	// Verify all hashes have orders
	for _, h := range hashes {
		if _, exists := orders[h]; !exists {
			t.Errorf("Hash %s not found in orders", h)
		}
	}
}

func TestHashRing_CIDRDistribution(t *testing.T) {
	hr := NewHashRing()

	// Add multiple hashes
	for i := 0; i < 4; i++ {
		if err := hr.AddHash(HashID(string(rune('a' + i)))); err != nil {
			t.Fatalf("Failed to add hash: %v", err)
		}
	}

	// Register a pool with a /24 (256 addresses)
	pool := IPPool{
		ID:      "pool1",
		Network: mustParseCIDR("10.0.0.0/24"),
	}
	if err := hr.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Verify each hash gets a subnet
	hashIDs := hr.GetHashIDs()
	totalSubnets := 0
	for hashID := range hashIDs {
		subnets, err := hr.GetHashIPNets(hashID)
		if err != nil {
			t.Errorf("Failed to get subnets for %s: %v", hashID, err)
			continue
		}
		if subnet, exists := subnets["pool1"]; exists {
			totalSubnets++
			// Verify the subnet is smaller than the original
			ones, _ := subnet.Mask.Size()
			if ones <= 24 {
				t.Errorf("Subnet %s should be smaller than /24", subnet)
			}
		}
	}

	if totalSubnets != 4 {
		t.Errorf("Expected 4 total subnets, got %d", totalSubnets)
	}
}

func TestHashRing_Concurrency(t *testing.T) {
	hr := NewHashRing()

	// Register a pool
	pool := IPPool{
		ID:      "pool1",
		Network: mustParseCIDR("192.168.0.0/16"),
	}
	if err := hr.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	var wg sync.WaitGroup
	const numGoroutines = 10

	// Concurrent AddHash operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			hashID := HashID(string(rune('a' + id)))
			if err := hr.AddHash(hashID); err != nil {
				t.Errorf("AddHash failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all hashes were added
	hashIDs := hr.GetHashIDs()
	if len(hashIDs) != numGoroutines {
		t.Errorf("Expected %d hashes, got %d", numGoroutines, len(hashIDs))
	}

	// Concurrent GetHashIPNets operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			hashID := HashID(string(rune('a' + id)))
			_, err := hr.GetHashIPNets(hashID)
			if err != nil {
				t.Errorf("GetHashIPNets failed: %v", err)
			}
		}(i)
	}

	wg.Wait()
}

func TestSplitCIDR(t *testing.T) {
	tests := []struct {
		name      string
		network   string
		parts     uint
		wantCount int
		wantErr   bool
	}{
		{
			name:      "split /24 into 4 parts",
			network:   "192.168.0.0/24",
			parts:     4,
			wantCount: 4,
			wantErr:   false,
		},
		{
			name:      "split /24 into 8 parts",
			network:   "192.168.0.0/24",
			parts:     8,
			wantCount: 8,
			wantErr:   false,
		},
		{
			name:      "split /24 into 1 part",
			network:   "192.168.0.0/24",
			parts:     1,
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "split /24 into 2 parts",
			network:   "192.168.0.0/24",
			parts:     2,
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:      "split /30 into 16 parts (too many)",
			network:   "192.168.0.0/30",
			parts:     16,
			wantCount: 0,
			wantErr:   true, // Cannot split further
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, network, err := net.ParseCIDR(tt.network)
			if err != nil {
				t.Fatalf("Failed to parse CIDR: %v", err)
			}

			subnets, err := splitCIDR(network, tt.parts)
			if (err != nil) != tt.wantErr {
				t.Errorf("splitCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(subnets) != tt.wantCount {
				t.Errorf("splitCIDR() returned %d subnets, want %d", len(subnets), tt.wantCount)
			}
		})
	}
}

func TestSplitCIDR_EmptyCIDR(t *testing.T) {
	// Test with nil IP
	_, err := splitCIDR(&net.IPNet{IP: nil, Mask: net.CIDRMask(24, 32)}, 4)
	if err == nil {
		t.Error("splitCIDR should return error for nil IP")
	}
	if !errors.Is(err, errEmptyCIDR) {
		t.Errorf("Expected errEmptyCIDR, got %v", err)
	}

	// Test with empty mask
	_, err = splitCIDR(&net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: nil}, 4)
	if err == nil {
		t.Error("splitCIDR should return error for nil mask")
	}

	// Test with empty mask slice
	_, err = splitCIDR(&net.IPNet{IP: net.ParseIP("192.168.0.0"), Mask: net.IPMask{}}, 4)
	if err == nil {
		t.Error("splitCIDR should return error for empty mask")
	}
}

func TestHashNotFoundError(t *testing.T) {
	err := &HashNotFoundError{HashID: "test-hash"}

	// Test Error() method
	expectedMsg := "hash test-hash does not exist in the hash ring"
	if err.Error() != expectedMsg {
		t.Errorf("Error() = %q, want %q", err.Error(), expectedMsg)
	}

	// Test Is() method
	otherErr := &HashNotFoundError{HashID: "other-hash"}
	if !errors.Is(err, otherErr) {
		t.Error("Is() should return true for HashNotFoundError")
	}

	// Test Is() with non-HashNotFoundError
	if errors.Is(err, errors.New("other error")) {
		t.Error("Is() should return false for non-HashNotFoundError")
	}
}

func TestCIDRSplitError(t *testing.T) {
	network := mustParseCIDR("192.168.0.0/24")
	err := &CIDRSplitError{Network: network, Parts: 5}

	// Test Error() method
	expectedMsg := "cannot split CIDR 192.168.0.0/24 into 5 parts"
	if err.Error() != expectedMsg {
		t.Errorf("Error() = %q, want %q", err.Error(), expectedMsg)
	}

	// Test Is() method
	otherErr := &CIDRSplitError{Network: network, Parts: 3}
	if !errors.Is(err, otherErr) {
		t.Error("Is() should return true for CIDRSplitError")
	}
}

func TestInvalidPoolError(t *testing.T) {
	err := InvalidPoolError{err: errors.New("test error"), poolID: "pool1"}

	// Test Error() method
	if err.Error() == "" {
		t.Error("Error() should not return empty string")
	}

	// Test GetPoolID() method
	if err.GetPoolID() != "pool1" {
		t.Errorf("GetPoolID() = %q, want %q", err.GetPoolID(), "pool1")
	}
}

func TestMakeHashOrder(t *testing.T) {
	hashes := []HashID{"c", "a", "b"}
	order := makeHashOrder(hashes)

	if len(order) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(order))
	}

	// Order should match input slice positions
	if order["c"] != 0 {
		t.Errorf("Expected c at position 0, got %d", order["c"])
	}
	if order["a"] != 1 {
		t.Errorf("Expected a at position 1, got %d", order["a"])
	}
	if order["b"] != 2 {
		t.Errorf("Expected b at position 2, got %d", order["b"])
	}
}

func TestSorted(t *testing.T) {
	input := map[HashID]struct{}{
		"c": {},
		"a": {},
		"b": {},
	}

	result := sorted(input)

	if len(result) != 3 {
		t.Errorf("Expected 3 elements, got %d", len(result))
	}

	// Should be sorted
	expected := []HashID{"a", "b", "c"}
	for i, h := range result {
		if h != expected[i] {
			t.Errorf("Position %d: expected %s, got %s", i, expected[i], h)
		}
	}
}

// Helper function to parse CIDR
func mustParseCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return network
}
