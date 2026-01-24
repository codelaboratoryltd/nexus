package hashring

import (
	"hash/fnv"
)

// JumpHash implements Google's Jump Consistent Hash algorithm.
// It provides consistent hashing with minimal key redistribution when
// the number of buckets changes.
//
// The algorithm guarantees that when the number of buckets changes from N to N+1,
// only 1/N+1 of the keys will be remapped, which is optimal.
//
// Parameters:
//   - key: A uint64 hash key to be mapped to a bucket
//   - numBuckets: The number of buckets to distribute keys across (must be > 0)
//
// Returns:
//   - The bucket index in range [0, numBuckets) that the key maps to
//
// Reference: "A Fast, Minimal Memory, Consistent Hash Algorithm"
// by John Lamping and Eric Veach (Google, 2014)
// https://arxiv.org/abs/1406.2294
func JumpHash(key uint64, numBuckets int) int32 {
	if numBuckets <= 0 {
		return 0
	}

	const jumpHashMagicMultiplier = 2862933555777941757
	const jumpHashShiftBits = 31
	const jumpHashKeyShift = 33

	var b int64 = -1
	var j int64

	for j < int64(numBuckets) {
		b = j
		key = key*jumpHashMagicMultiplier + 1
		j = int64(float64(b+1) * (float64(int64(1)<<jumpHashShiftBits) / float64((key>>jumpHashKeyShift)+1)))
	}

	return int32(b)
}

// RendezvousHash implements Highest Random Weight (HRW) hashing, also known as Rendezvous hashing.
// This algorithm provides minimal disruption when nodes are added or removed from arbitrary positions.
//
// Unlike Jump Hash which requires sequential bucket numbering (0..N), Rendezvous Hash works with
// arbitrary node identifiers, making it ideal for distributed systems where nodes can be
// added/removed at any position.
//
// The algorithm computes a hash score for each (vnode, node) pair and selects the node with
// the highest score. This ensures that when a node is removed, only vnodes assigned to that
// node are redistributed.
//
// Parameters:
//   - vnodeID: The virtual node ID to assign
//   - nodes: List of available node IDs
//
// Returns:
//   - The node ID with the highest hash score for this vnode
func RendezvousHash(vnodeID uint64, nodes []NodeID) NodeID {
	if len(nodes) == 0 {
		return ""
	}
	if len(nodes) == 1 {
		return nodes[0]
	}

	var bestNode NodeID
	var bestHash uint64

	for _, node := range nodes {
		hash := hashCombine(vnodeID, string(node))
		if hash > bestHash {
			bestHash = hash
			bestNode = node
		}
	}

	return bestNode
}

// RendezvousHashString is a convenience wrapper that takes a string vnode ID.
func RendezvousHashString(vnodeID string, nodes []NodeID) NodeID {
	return RendezvousHash(hashString(vnodeID), nodes)
}

// hashCombine creates a combined hash score for Rendezvous hashing.
// Uses Wang's 64-bit hash mixer for excellent distribution.
func hashCombine(vnodeID uint64, nodeName string) uint64 {
	// First hash the node name to a 64-bit value
	nodeHash := hashString(nodeName)

	// XOR the vnode ID with node hash
	combined := vnodeID ^ nodeHash

	// Apply Wang's 64-bit hash mixer for better distribution
	// Reference: Thomas Wang's integer hash functions
	combined = (^combined) + (combined << 21)
	combined = combined ^ (combined >> 24)
	combined = (combined + (combined << 3)) + (combined << 8)
	combined = combined ^ (combined >> 14)
	combined = (combined + (combined << 2)) + (combined << 4)
	combined = combined ^ (combined >> 28)
	combined = combined + (combined << 31)

	return combined
}

// hashString converts a string to uint64 using FNV-1a.
func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}
