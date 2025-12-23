package hashring

import "net"

// DefaultVNodesCount is the default number of virtual nodes per pool
const DefaultVNodesCount = 6

// PoolID represents a pool identifier
type PoolID string

// NodeID represents a node identifier in the cluster
type NodeID string

// HashID represents a hash identifier in the ring
type HashID string

// VirtualNodeID represents a virtual node identifier
type VirtualNodeID string

// IPPool represents an IP Pool tracked by a hashring
type IPPool struct {
	ID          PoolID
	Network     *net.IPNet
	VNodesCount int
}

// PoolSubnetsSliceMap represents the collection of subnets belonging to specific pools
type PoolSubnetsSliceMap map[PoolID][]*net.IPNet
