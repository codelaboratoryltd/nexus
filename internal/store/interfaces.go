package store

import (
	"context"

	"github.com/codelaboratoryltd/nexus/internal/resource"

	ds "github.com/ipfs/go-datastore"
)

// AllocationStore manages IP allocations.
type AllocationStore interface {
	SaveAllocation(ctx context.Context, a *Allocation) error
	RemoveAllocation(ctx context.Context, poolID, subscriberID string) error
	GetAllocation(ctx context.Context, poolID, subscriberID string) (*Allocation, error)
	LoadAllocatorState(ctx context.Context, poolID string, allocator resource.Allocator) error
	GetAllocationBySubscriber(ctx context.Context, subscriberID string) (*Allocation, error)
	UnmarshalAllocationKey(key ds.Key) (*AllocationKey, error)
	UnmarshalSubscriberKey(key ds.Key, subscriberID string) (*AllocationKey, error)
	ListAllocationsByPool(ctx context.Context, poolID string) (allocs []*Allocation, err error)
	CountAllocationsByPool(ctx context.Context, poolID string) (int, error)

	// Backup allocation methods for failover support
	ListBackupAllocationsByNode(ctx context.Context, nodeID string) ([]*Allocation, error)
	ListAllocationsByNode(ctx context.Context, nodeID string) ([]*Allocation, error)
	AssignBackupNode(ctx context.Context, poolID, subscriberID, backupNodeID string) error
}

// PoolStore manages resource pools.
type PoolStore interface {
	SavePool(ctx context.Context, pool *Pool) error
	GetPool(ctx context.Context, poolID string) (*Pool, error)
	DeletePool(ctx context.Context, poolID string) error
	ListPools(ctx context.Context) ([]*Pool, error)
	UnmarshalKey(key ds.Key, value []byte) (*Pool, error)
}

// NodeStore manages cluster nodes.
type NodeStore interface {
	ListNodes(ctx context.Context) ([]*Node, error)
}

// StateStore provides access to distributed state (CLSet).
type StateStore interface {
	GetMembers(ctx context.Context) map[string]*NodeMember
}

// NodeMember represents a member in the cluster state.
type NodeMember struct {
	BestBefore uint64
	Metadata   map[string]string
}
