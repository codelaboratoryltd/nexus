package hashring

import (
	"net"
	"sync"
	"testing"
)

func TestNewVirtualNodesHashRing(t *testing.T) {
	ring := NewVirtualNodesHashRing()
	if ring == nil {
		t.Fatal("NewVirtualNodesHashRing returned nil")
	}
	if ring.nodes == nil {
		t.Error("nodes map is nil")
	}
	if ring.virtualNodes == nil {
		t.Error("virtualNodes map is nil")
	}
	if ring.poolsHashRings == nil {
		t.Error("poolsHashRings map is nil")
	}
}

func TestVirtualHashRing_AddRemoveNode(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add a node
	if err := ring.AddNode("node1"); err != nil {
		t.Errorf("AddNode() error = %v", err)
	}

	// Verify node was added
	nodes := ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(nodes))
	}
	if nodes[0] != "node1" {
		t.Errorf("Expected node1, got %s", nodes[0])
	}

	// Add same node again (idempotent)
	if err := ring.AddNode("node1"); err != nil {
		t.Errorf("AddNode() for existing node error = %v", err)
	}
	nodes = ring.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node after duplicate add, got %d", len(nodes))
	}

	// Remove the node
	if err := ring.RemoveNode("node1"); err != nil {
		t.Errorf("RemoveNode() error = %v", err)
	}

	// Verify node was removed
	nodes = ring.ListNodes()
	if len(nodes) != 0 {
		t.Errorf("Expected 0 nodes after removal, got %d", len(nodes))
	}

	// Remove non-existent node (idempotent)
	if err := ring.RemoveNode("nonexistent"); err != nil {
		t.Errorf("RemoveNode() for non-existent node error = %v", err)
	}
}

func TestVirtualHashRing_RegisterUnregisterPool(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add a node first
	if err := ring.AddNode("node1"); err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	// Register a pool
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("192.168.0.0/24"),
		VNodesCount: 4,
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Errorf("RegisterPool() error = %v", err)
	}

	// Verify pool was registered
	pools := ring.GetIPPools()
	if len(pools) != 1 {
		t.Errorf("Expected 1 pool, got %d", len(pools))
	}

	// Register same pool again (idempotent)
	if err := ring.RegisterPool(pool); err != nil {
		t.Errorf("RegisterPool() for existing pool error = %v", err)
	}

	// Unregister the pool
	if err := ring.UnregisterPool("pool1"); err != nil {
		t.Errorf("UnregisterPool() error = %v", err)
	}

	// Verify pool was removed
	pools = ring.GetIPPools()
	if len(pools) != 0 {
		t.Errorf("Expected 0 pools after unregistration, got %d", len(pools))
	}

	// Unregister non-existent pool (idempotent)
	if err := ring.UnregisterPool("nonexistent"); err != nil {
		t.Errorf("UnregisterPool() for non-existent pool error = %v", err)
	}
}

func TestVirtualHashRing_RegisterPoolEmptyID(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	pool := IPPool{
		ID:      "",
		Network: mustParseCIDR("192.168.0.0/24"),
	}

	err := ring.RegisterPool(pool)
	if err == nil {
		t.Error("RegisterPool() should return error for empty pool ID")
	}
}

func TestVirtualHashRing_AddPoolRemovePool(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add a node
	if err := ring.AddNode("node1"); err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	// Use convenience method to add pool
	network := mustParseCIDR("10.0.0.0/24")
	if err := ring.AddPool("pool1", network); err != nil {
		t.Errorf("AddPool() error = %v", err)
	}

	// Verify pool exists
	pools := ring.GetIPPools()
	if len(pools) != 1 {
		t.Errorf("Expected 1 pool, got %d", len(pools))
	}

	// Use convenience method to remove pool
	if err := ring.RemovePool("pool1"); err != nil {
		t.Errorf("RemovePool() error = %v", err)
	}

	// Verify pool was removed
	pools = ring.GetIPPools()
	if len(pools) != 0 {
		t.Errorf("Expected 0 pools after removal, got %d", len(pools))
	}
}

func TestVirtualHashRing_ListPoolSubnetsAtNode(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add nodes
	for _, nodeID := range []string{"node1", "node2"} {
		if err := ring.AddNode(NodeID(nodeID)); err != nil {
			t.Fatalf("Failed to add node: %v", err)
		}
	}

	// Register a pool
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("10.0.0.0/16"),
		VNodesCount: 4,
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Get subnets for node1
	subnets, err := ring.ListPoolSubnetsAtNode("node1")
	if err != nil {
		t.Errorf("ListPoolSubnetsAtNode() error = %v", err)
	}
	if subnets == nil {
		t.Error("ListPoolSubnetsAtNode() returned nil")
	}

	// Get subnets for non-existent node
	_, err = ring.ListPoolSubnetsAtNode("nonexistent")
	if err == nil {
		t.Error("ListPoolSubnetsAtNode() should return error for non-existent node")
	}
}

func TestVirtualHashRing_AllocateIP(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add a node
	if err := ring.AddNode("node1"); err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	// Register a pool
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("192.168.0.0/24"),
		VNodesCount: 4,
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Allocate IP for subscriber
	ip := ring.AllocateIP("pool1", "subscriber1")
	if ip == nil {
		t.Error("AllocateIP() returned nil")
	}

	// Verify IP is within the pool's network
	network := mustParseCIDR("192.168.0.0/24")
	if ip != nil && !network.Contains(ip) {
		t.Errorf("Allocated IP %s is not in network %s", ip, network)
	}

	// Different subscribers may get different IPs
	ip3 := ring.AllocateIP("pool1", "subscriber2")
	if ip3 == nil {
		t.Error("AllocateIP() for different subscriber returned nil")
	}

	// Verify IP3 is also within the pool's network
	if ip3 != nil && !network.Contains(ip3) {
		t.Errorf("Allocated IP %s is not in network %s", ip3, network)
	}
}

func TestVirtualHashRing_AllocateIP_NonExistentPool(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Try to allocate from non-existent pool
	ip := ring.AllocateIP("nonexistent", "subscriber1")
	if ip != nil {
		t.Error("AllocateIP() should return nil for non-existent pool")
	}
}

func TestVirtualHashRing_AllocateIP_WithDefaultVNodes(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add a node first (required for allocation to work)
	if err := ring.AddNode("node1"); err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	// Register pool with VNodesCount=0 (should use default)
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("192.168.0.0/24"),
		VNodesCount: 0, // This will use DefaultVNodesCount (6)
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Allocate should work since default vnodes are used
	ip := ring.AllocateIP("pool1", "subscriber1")
	if ip == nil {
		t.Error("AllocateIP() should return an IP when default vnodes are used")
	}

	// Verify IP is within the pool's network
	network := mustParseCIDR("192.168.0.0/24")
	if ip != nil && !network.Contains(ip) {
		t.Errorf("Allocated IP %s is not in network %s", ip, network)
	}
}

func TestVirtualHashRing_GetHashMapping(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Add nodes
	if err := ring.AddNode("node1"); err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	// Register pool
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("10.0.0.0/24"),
		VNodesCount: 2,
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	// Get hash mapping
	mapping := ring.GetHashMapping()
	if mapping.NodePoolVNode == nil {
		t.Error("NodePoolVNode map is nil")
	}
	if mapping.PoolVNodeNode == nil {
		t.Error("PoolVNodeNode map is nil")
	}
}

func TestVirtualHashRing_Concurrency(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Register initial pool
	pool := IPPool{
		ID:          "pool1",
		Network:     mustParseCIDR("10.0.0.0/8"),
		VNodesCount: 8,
	}
	if err := ring.RegisterPool(pool); err != nil {
		t.Fatalf("Failed to register pool: %v", err)
	}

	var wg sync.WaitGroup
	const numGoroutines = 10

	// Concurrent AddNode operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			nodeID := NodeID(string(rune('a' + id)))
			if err := ring.AddNode(nodeID); err != nil {
				t.Errorf("AddNode failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all nodes were added
	nodes := ring.ListNodes()
	if len(nodes) != numGoroutines {
		t.Errorf("Expected %d nodes, got %d", numGoroutines, len(nodes))
	}

	// Concurrent AllocateIP operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			subscriberID := string(rune('0' + id))
			ip := ring.AllocateIP("pool1", subscriberID)
			if ip == nil {
				t.Errorf("AllocateIP returned nil for subscriber %s", subscriberID)
			}
		}(i)
	}

	wg.Wait()
}

func TestVirtualHashRing_ListVirtualNodes(t *testing.T) {
	ring := NewVirtualNodesHashRing()

	// Initially should be empty
	vnodes := ring.ListVirtualNodes()
	if len(vnodes) != 0 {
		t.Errorf("Expected 0 virtual nodes initially, got %d", len(vnodes))
	}
}

func TestHashMapping(t *testing.T) {
	mapping := NewVirtualHashMapping()

	// Test AddNode
	mapping.AddNode("node1")
	if !mapping.HasNode("node1") {
		t.Error("HasNode should return true for added node")
	}
	if mapping.HasNode("node2") {
		t.Error("HasNode should return false for non-existent node")
	}

	// Test Link
	mapping.Link("node1", "pool1", "vnode1")
	if !mapping.HasVirtualHash("pool1", "vnode1") {
		t.Error("HasVirtualHash should return true for linked virtual hash")
	}

	// Test ListOwnedVirtualHashes
	owned := mapping.ListOwnedVirtualHashes("node1")
	if owned == nil {
		t.Error("ListOwnedVirtualHashes returned nil")
	}
	if _, exists := owned["pool1"]; !exists {
		t.Error("Pool1 should be in owned virtual hashes")
	}

	// Test for non-existent node
	owned = mapping.ListOwnedVirtualHashes("nonexistent")
	if owned != nil {
		t.Error("ListOwnedVirtualHashes should return nil for non-existent node")
	}

	// Test Unlink
	mapping.Unlink("node1", "pool1", "vnode1")
	if mapping.HasVirtualHash("pool1", "vnode1") {
		t.Error("HasVirtualHash should return false after unlink")
	}

	// Test RemoveVirtualHash
	mapping.Link("node1", "pool2", "vnode2")
	mapping.RemoveVirtualHash("pool2", "vnode2")
	if mapping.HasVirtualHash("pool2", "vnode2") {
		t.Error("HasVirtualHash should return false after RemoveVirtualHash")
	}
}

func TestHashMapping_Copy(t *testing.T) {
	original := NewVirtualHashMapping()
	original.AddNode("node1")
	original.Link("node1", "pool1", "vnode1")

	// Copy the mapping
	copied := original.Copy()

	// Verify copy has same content
	if !copied.HasNode("node1") {
		t.Error("Copy should have node1")
	}
	if !copied.HasVirtualHash("pool1", "vnode1") {
		t.Error("Copy should have vnode1")
	}

	// Modify original and verify copy is independent
	original.Link("node1", "pool1", "vnode2")
	if copied.HasVirtualHash("pool1", "vnode2") {
		t.Error("Copy should not be affected by changes to original")
	}
}

func TestHashString(t *testing.T) {
	// Test determinism
	hash1 := hashString("test")
	hash2 := hashString("test")
	if hash1 != hash2 {
		t.Error("hashString is not deterministic")
	}

	// Test different inputs produce different hashes
	hash3 := hashString("other")
	if hash1 == hash3 {
		t.Error("hashString should produce different values for different inputs")
	}

	// Test empty string
	hashEmpty := hashString("")
	if hashEmpty == 0 {
		t.Error("hashString should handle empty string")
	}
}

func TestIPFromSubnetAndHash(t *testing.T) {
	tests := []struct {
		name    string
		subnet  string
		hash    uint64
		wantNil bool
	}{
		{
			name:    "valid IPv4 subnet",
			subnet:  "192.168.0.0/24",
			hash:    100,
			wantNil: false,
		},
		{
			name:    "small subnet /30",
			subnet:  "192.168.0.0/30",
			hash:    0,
			wantNil: false,
		},
		{
			name:    "/32 subnet (single host)",
			subnet:  "192.168.0.1/32",
			hash:    0,
			wantNil: true, // No host bits
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, subnet, err := net.ParseCIDR(tt.subnet)
			if err != nil {
				t.Fatalf("Failed to parse CIDR: %v", err)
			}

			ip := ipFromSubnetAndHash(subnet, tt.hash)
			if (ip == nil) != tt.wantNil {
				t.Errorf("ipFromSubnetAndHash() = %v, wantNil %v", ip, tt.wantNil)
			}

			if ip != nil && !subnet.Contains(ip) {
				t.Errorf("IP %s is not in subnet %s", ip, subnet)
			}
		})
	}
}

func TestIPFromSubnetAndHash_NilSubnet(t *testing.T) {
	ip := ipFromSubnetAndHash(nil, 100)
	if ip != nil {
		t.Error("ipFromSubnetAndHash should return nil for nil subnet")
	}

	ip = ipFromSubnetAndHash(&net.IPNet{IP: nil, Mask: net.CIDRMask(24, 32)}, 100)
	if ip != nil {
		t.Error("ipFromSubnetAndHash should return nil for nil IP")
	}
}

func TestKeys(t *testing.T) {
	input := map[NodeID]struct{}{
		"node1": {},
		"node2": {},
		"node3": {},
	}

	result := keys(input)
	if len(result) != 3 {
		t.Errorf("Expected 3 elements, got %d", len(result))
	}

	// Verify all keys are present (order may vary)
	found := make(map[NodeID]bool)
	for _, k := range result {
		found[k] = true
	}
	for k := range input {
		if !found[k] {
			t.Errorf("Key %s not found in result", k)
		}
	}
}

func TestDefaultVNodesCount(t *testing.T) {
	if DefaultVNodesCount != 6 {
		t.Errorf("DefaultVNodesCount = %d, want 6", DefaultVNodesCount)
	}
}
