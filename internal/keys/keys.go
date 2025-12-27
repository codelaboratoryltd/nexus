package keys

import ds "github.com/ipfs/go-datastore"

// Datastore key prefixes for different resource types.
var (
	NexusKey      = ds.NewKey("nexus")      // Root key for CRDT namespace
	PoolKey       = ds.NewKey("pool")       // Pool data
	AllocationKey = ds.NewKey("allocation") // IP allocations by pool/subscriber
	SubscriberKey = ds.NewKey("subscriber") // Subscriber index
	NodeKey       = ds.NewKey("node")       // Node metadata
)
