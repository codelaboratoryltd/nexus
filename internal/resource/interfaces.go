package resource

import (
	"context"
)

// Resource represents any allocatable resource (IP, Port, VLAN, etc.)
// It provides a common abstraction for different resource types.
type Resource interface {
	// String returns a human-readable representation of the resource
	String() string

	// Bytes returns a serialized byte representation for storage
	Bytes() []byte

	// Equal checks if two resources are equal
	Equal(Resource) bool

	// Type returns the resource type identifier (e.g., "ipv4", "port", "vlan")
	Type() string
}

// Allocator manages allocation for a specific resource pool.
// This interface is implemented by specific allocators (IPv4Allocator, PortAllocator, etc.)
type Allocator interface {
	// Allocate returns the next available resource
	Allocate(ctx context.Context) (Resource, error)

	// Deallocate marks a resource as available
	Deallocate(ctx context.Context, resource Resource) error

	// Reserve marks a specific resource as allocated
	Reserve(ctx context.Context, resource Resource) error

	// Contains checks if resource is in this allocator's range
	Contains(resource Resource) bool

	// Available returns true if allocator has free resources
	Available() bool

	// Count returns (allocated, total) resource counts
	Count() (allocated, total uint64)
}

// Range defines a contiguous range of allocatable resources.
type Range interface {
	// Contains checks if a resource is within this range
	Contains(Resource) bool

	// Split divides the range into n equal sub-ranges for sharding
	Split(n int) ([]Range, error)

	// Size returns the total number of resources in this range
	Size() uint64

	// String returns a human-readable representation
	String() string
}

// Pool describes a pool of allocatable resources with metadata.
type Pool interface {
	// ID returns the unique pool identifier
	ID() string

	// Type returns the resource type (e.g., "ipv4", "port")
	Type() string

	// Range returns the main resource range for this pool
	Range() Range

	// ShardingFactor returns the number of shards for distribution
	ShardingFactor() int

	// Metadata returns custom metadata
	Metadata() map[string]string

	// Validate checks if the pool configuration is valid
	Validate() error

	// Config returns the raw configuration for persistence
	Config() map[string]interface{}
}

// Type defines the behavior and factory methods for a resource type.
// Each resource type (IPv4, Port, VLAN) implements this interface.
type Type interface {
	// Name returns the resource type identifier (e.g., "ipv4", "port", "vlan")
	Name() string

	// NewPool creates a resource pool from configuration
	NewPool(config map[string]interface{}) (Pool, error)

	// NewAllocator creates an allocator for specific resource ranges
	// The ranges parameter represents the shards assigned to this node
	NewAllocator(pool Pool, ranges []Range) (Allocator, error)

	// ParseResource converts a string representation to a Resource
	ParseResource(s string) (Resource, error)

	// ParseRange converts a string representation to a Range
	ParseRange(s string) (Range, error)
}

// Registry holds all registered resource types.
type Registry struct {
	types map[string]Type
}

// NewRegistry creates a new resource type registry.
func NewRegistry() *Registry {
	return &Registry{
		types: make(map[string]Type),
	}
}

// Register adds a resource type to the registry.
func (r *Registry) Register(rt Type) {
	r.types[rt.Name()] = rt
}

// Get retrieves a resource type by name.
func (r *Registry) Get(name string) (Type, bool) {
	rt, ok := r.types[name]
	return rt, ok
}

// List returns all registered resource type names.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	return names
}
