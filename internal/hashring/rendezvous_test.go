package hashring

import (
	"fmt"
	"testing"
)

func TestJumpHash(t *testing.T) {
	tests := []struct {
		name        string
		key         uint64
		numBuckets  int
		wantInRange bool
	}{
		{"single bucket", 12345, 1, true},
		{"two buckets", 12345, 2, true},
		{"ten buckets", 12345, 10, true},
		{"hundred buckets", 12345, 100, true},
		{"zero buckets returns 0", 12345, 0, true},
		{"negative buckets returns 0", 12345, -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JumpHash(tt.key, tt.numBuckets)
			if tt.numBuckets <= 0 {
				if got != 0 {
					t.Errorf("JumpHash() = %v, want 0 for invalid numBuckets", got)
				}
				return
			}
			if got < 0 || int(got) >= tt.numBuckets {
				t.Errorf("JumpHash() = %v, want in range [0, %d)", got, tt.numBuckets)
			}
		})
	}
}

func TestJumpHash_Consistency(t *testing.T) {
	// Same key should always map to same bucket
	key := uint64(42)
	numBuckets := 10

	expected := JumpHash(key, numBuckets)
	for i := 0; i < 100; i++ {
		got := JumpHash(key, numBuckets)
		if got != expected {
			t.Errorf("JumpHash not consistent: got %v, want %v", got, expected)
		}
	}
}

func TestJumpHash_MinimalDisruption(t *testing.T) {
	// When adding a bucket, most keys should stay in same bucket
	numKeys := 10000
	initialBuckets := 10
	newBuckets := 11

	movedCount := 0
	for key := uint64(0); key < uint64(numKeys); key++ {
		oldBucket := JumpHash(key, initialBuckets)
		newBucket := JumpHash(key, newBuckets)
		if oldBucket != newBucket {
			movedCount++
		}
	}

	// With optimal consistent hashing, ~1/(N+1) = ~9% should move
	// Allow some tolerance
	expectedMoved := numKeys / newBuckets
	tolerance := numKeys / 20 // 5% tolerance

	if movedCount < expectedMoved-tolerance || movedCount > expectedMoved+tolerance {
		t.Errorf("JumpHash moved %d keys, expected ~%d (±%d)", movedCount, expectedMoved, tolerance)
	}
}

func TestRendezvousHash(t *testing.T) {
	nodes := []NodeID{"node-a", "node-b", "node-c"}

	tests := []struct {
		name    string
		vnodeID uint64
		nodes   []NodeID
	}{
		{"basic assignment", 1, nodes},
		{"different vnode", 2, nodes},
		{"large vnode id", 999999, nodes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RendezvousHash(tt.vnodeID, tt.nodes)
			if got == "" {
				t.Errorf("RendezvousHash() returned empty string")
			}

			// Verify it's one of the nodes
			found := false
			for _, n := range tt.nodes {
				if n == got {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("RendezvousHash() = %v, not in nodes list", got)
			}
		})
	}
}

func TestRendezvousHash_EmptyNodes(t *testing.T) {
	got := RendezvousHash(1, []NodeID{})
	if got != "" {
		t.Errorf("RendezvousHash() with empty nodes = %v, want empty string", got)
	}
}

func TestRendezvousHash_SingleNode(t *testing.T) {
	node := NodeID("only-node")
	got := RendezvousHash(1, []NodeID{node})
	if got != node {
		t.Errorf("RendezvousHash() with single node = %v, want %v", got, node)
	}
}

func TestRendezvousHash_Consistency(t *testing.T) {
	nodes := []NodeID{"node-a", "node-b", "node-c", "node-d", "node-e"}
	vnodeID := uint64(12345)

	expected := RendezvousHash(vnodeID, nodes)
	for i := 0; i < 100; i++ {
		got := RendezvousHash(vnodeID, nodes)
		if got != expected {
			t.Errorf("RendezvousHash not consistent: got %v, want %v", got, expected)
		}
	}
}

func TestRendezvousHash_MinimalDisruption_NodeRemoved(t *testing.T) {
	// When a node is removed, only vnodes assigned to that node should move
	nodes := []NodeID{"node-a", "node-b", "node-c", "node-d", "node-e"}
	nodesWithoutC := []NodeID{"node-a", "node-b", "node-d", "node-e"}

	numVnodes := 1000
	movedCount := 0
	movedFromC := 0

	for vnode := uint64(0); vnode < uint64(numVnodes); vnode++ {
		oldNode := RendezvousHash(vnode, nodes)
		newNode := RendezvousHash(vnode, nodesWithoutC)

		if oldNode != newNode {
			movedCount++
			if oldNode == "node-c" {
				movedFromC++
			}
		}
	}

	// All moved vnodes should have been on node-c
	if movedCount != movedFromC {
		t.Errorf("RendezvousHash moved %d vnodes, but only %d were on removed node", movedCount, movedFromC)
	}

	// Approximately 1/5 = 20% should have been on node-c
	expectedOnC := numVnodes / 5
	tolerance := numVnodes / 20 // 5% tolerance

	if movedFromC < expectedOnC-tolerance || movedFromC > expectedOnC+tolerance {
		t.Errorf("RendezvousHash had %d vnodes on node-c, expected ~%d", movedFromC, expectedOnC)
	}
}

func TestRendezvousHash_MinimalDisruption_NodeAdded(t *testing.T) {
	// When a node is added, only ~1/N vnodes should move to it
	nodes := []NodeID{"node-a", "node-b", "node-c", "node-d"}
	nodesWithE := []NodeID{"node-a", "node-b", "node-c", "node-d", "node-e"}

	numVnodes := 1000
	movedCount := 0
	movedToE := 0

	for vnode := uint64(0); vnode < uint64(numVnodes); vnode++ {
		oldNode := RendezvousHash(vnode, nodes)
		newNode := RendezvousHash(vnode, nodesWithE)

		if oldNode != newNode {
			movedCount++
			if newNode == "node-e" {
				movedToE++
			}
		}
	}

	// All moved vnodes should now be on node-e
	if movedCount != movedToE {
		t.Errorf("RendezvousHash moved %d vnodes, but only %d went to new node", movedCount, movedToE)
	}

	// Approximately 1/5 = 20% should move to node-e
	expectedToE := numVnodes / 5
	tolerance := numVnodes / 20 // 5% tolerance

	if movedToE < expectedToE-tolerance || movedToE > expectedToE+tolerance {
		t.Errorf("RendezvousHash moved %d vnodes to node-e, expected ~%d", movedToE, expectedToE)
	}
}

func TestRendezvousHash_Distribution(t *testing.T) {
	// Vnodes should be roughly evenly distributed across nodes
	nodes := []NodeID{"node-a", "node-b", "node-c", "node-d", "node-e"}
	numVnodes := 10000

	distribution := make(map[NodeID]int)
	for vnode := uint64(0); vnode < uint64(numVnodes); vnode++ {
		node := RendezvousHash(vnode, nodes)
		distribution[node]++
	}

	expectedPerNode := numVnodes / len(nodes)
	tolerance := numVnodes / 20 // 5% tolerance

	for node, count := range distribution {
		if count < expectedPerNode-tolerance || count > expectedPerNode+tolerance {
			t.Errorf("Node %s has %d vnodes, expected ~%d (±%d)", node, count, expectedPerNode, tolerance)
		}
	}
}

func TestRendezvousHashString(t *testing.T) {
	nodes := []NodeID{"node-a", "node-b", "node-c"}
	vnodeID := "vnode-123"

	got := RendezvousHashString(vnodeID, nodes)
	if got == "" {
		t.Errorf("RendezvousHashString() returned empty string")
	}

	// Should be consistent
	for i := 0; i < 10; i++ {
		if RendezvousHashString(vnodeID, nodes) != got {
			t.Errorf("RendezvousHashString() not consistent")
		}
	}
}

// Benchmarks

func BenchmarkJumpHash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		JumpHash(uint64(i), 100)
	}
}

func BenchmarkRendezvousHash_5Nodes(b *testing.B) {
	nodes := []NodeID{"node-a", "node-b", "node-c", "node-d", "node-e"}
	for i := 0; i < b.N; i++ {
		RendezvousHash(uint64(i), nodes)
	}
}

func BenchmarkRendezvousHash_100Nodes(b *testing.B) {
	nodes := make([]NodeID, 100)
	for i := range nodes {
		nodes[i] = NodeID(fmt.Sprintf("node-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RendezvousHash(uint64(i), nodes)
	}
}
