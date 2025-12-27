package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"

	"github.com/codelaboratoryltd/nexus/internal/resource"
	"github.com/codelaboratoryltd/nexus/internal/util"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// allocationStore handles persistence of IP allocations.
type allocationStore struct {
	store                ds.Batching
	poolStore            PoolStore
	poolAllocationPrefix ds.Key
	subscriberPrefix     ds.Key
}

// NewAllocationStore creates a new allocation store.
func NewAllocationStore(store ds.Batching, poolAllocationPrefix, subscriberPrefix ds.Key, poolstore PoolStore) *allocationStore {
	return &allocationStore{
		store:                store,
		poolStore:            poolstore,
		poolAllocationPrefix: poolAllocationPrefix,
		subscriberPrefix:     subscriberPrefix,
	}
}

// SaveAllocation persists an allocation to the datastore.
func (as *allocationStore) SaveAllocation(ctx context.Context, a *Allocation) error {
	p, err := as.poolStore.GetPool(ctx, a.PoolID)
	if err != nil {
		return fmt.Errorf("getting pool: %w", err)
	}

	// Calculate the offset from the IP
	o, err := util.IPToOffset(&p.CIDR, a.IP)
	if err != nil {
		return fmt.Errorf("calculating offset: %w", err)
	}

	// Because indices are 0-based, the first IP would be 0 and therefore an empty byte array
	// Fix this by adding 1
	o.Add(o, big.NewInt(1))

	// Encode offset as base64
	offsetBase64 := base64.RawURLEncoding.EncodeToString(o.Bytes())

	// Construct keys
	allocationKey := as.poolAllocationPrefix.ChildString(a.PoolID).ChildString(a.SubscriberID).ChildString(offsetBase64)
	subscriberKey := as.subscriberPrefix.ChildString(a.SubscriberID).ChildString(a.PoolID).ChildString(offsetBase64)

	// Serialize timestamp as raw bytes
	timestampBytes := util.MarshalTime(a.Timestamp)

	// Use batch to persist both entries
	b, err := as.store.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to batch store allocation: %w", err)
	}

	// Store the allocation
	if err := b.Put(ctx, allocationKey, timestampBytes); err != nil {
		return fmt.Errorf("failed to persist allocation: %w", err)
	}

	// Store the subscriber index
	if err := b.Put(ctx, subscriberKey, timestampBytes); err != nil {
		return fmt.Errorf("failed to update subscriber index: %w", err)
	}

	// Commit the batch
	if err := b.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit allocation: %w", err)
	}

	return nil
}

// RemoveAllocation deletes an allocation from the datastore.
func (as *allocationStore) RemoveAllocation(ctx context.Context, poolID, subscriberID string) error {
	a, _, err := as.findAllocationKey(ctx, poolID, subscriberID)
	if err != nil {
		return err
	}

	offsetBase64 := base64.RawURLEncoding.EncodeToString(a.IPOffset.Bytes())

	// Construct keys
	allocationKey := as.poolAllocationPrefix.ChildString(poolID).ChildString(subscriberID).ChildString(offsetBase64)
	subscriberKey := as.subscriberPrefix.ChildString(subscriberID).ChildString(poolID).ChildString(offsetBase64)

	b, err := as.store.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to batch store allocation: %w", err)
	}

	// Remove keys
	if err := b.Delete(ctx, allocationKey); err != nil {
		return fmt.Errorf("failed to delete allocation: %w", err)
	}
	if err := b.Delete(ctx, subscriberKey); err != nil {
		return fmt.Errorf("failed to remove subscriber index: %w", err)
	}

	// Commit the batch
	if err := b.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit deallocation: %w", err)
	}

	return nil
}

// GetAllocation retrieves the IP allocated to a subscriber in a pool.
func (as *allocationStore) GetAllocation(ctx context.Context, poolID, subscriberID string) (*Allocation, error) {
	p, err := as.poolStore.GetPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("getting pool: %w", err)
	}

	allocationKey, data, err := as.findAllocationKey(ctx, poolID, subscriberID)
	if err != nil {
		return nil, err
	}

	return allocationKey.GetAllocation(p, data)
}

// findAllocationKey searches for an allocation key.
func (as *allocationStore) findAllocationKey(ctx context.Context, poolID, subscriberID string) (*AllocationKey, []byte, error) {
	prefix := as.poolAllocationPrefix.ChildString(poolID).ChildString(subscriberID)
	q := query.Query{Prefix: prefix.String()}
	results, err := as.store.Query(ctx, q)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query allocation: %w", err)
	}
	defer results.Close()

	for result := range results.Next() {
		if result.Value == nil {
			continue
		}
		key := ds.RawKey(result.Key)

		allocationKey, err := as.UnmarshalAllocationKey(key)
		if err != nil {
			if errors.Is(err, ErrMalformedKey) {
				continue
			}
			return nil, nil, fmt.Errorf("failed to unmarshal allocation key: %w", err)
		}

		return allocationKey, result.Value, nil
	}

	return nil, nil, fmt.Errorf("no allocation found for subscriber %s in pool %s: %w", subscriberID, poolID, ErrNoAllocationFound)
}

// LoadAllocatorState loads allocation state into an allocator.
func (as *allocationStore) LoadAllocatorState(ctx context.Context, poolID string, allocator resource.Allocator) error {
	results, err := as.ListAllocationsByPool(ctx, poolID)
	if err != nil {
		return fmt.Errorf("failed to list allocations: %w", err)
	}

	for _, a := range results {
		// Reserve the allocated IP in the allocator
		if err := allocator.Reserve(ctx, resource.IPResource(a.IP)); err != nil {
			return fmt.Errorf("failed to reserve IP %s: %w", a.IP, err)
		}
	}
	return nil
}

// GetAllocationBySubscriber retrieves a subscriber's allocation across all pools.
func (as *allocationStore) GetAllocationBySubscriber(ctx context.Context, subscriberID string) (*Allocation, error) {
	prefix := as.subscriberPrefix.ChildString(subscriberID)
	q := query.Query{Prefix: prefix.String()}
	results, err := as.store.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscriber allocation: %w", err)
	}
	defer results.Close()

	for result := range results.Next() {
		if result.Value == nil {
			continue
		}
		key := ds.RawKey(result.Key)
		ar, err := as.UnmarshalSubscriberKey(key, subscriberID)
		if err != nil {
			if errors.Is(err, ErrMalformedKey) {
				continue
			}
			return nil, fmt.Errorf("failed to unmarshal allocation key: %w", err)
		}

		p, err := as.poolStore.GetPool(ctx, ar.PoolID)
		if err != nil {
			return nil, fmt.Errorf("getting pool: %w", err)
		}

		return ar.GetAllocation(p, result.Value)
	}
	return nil, ErrNoAllocationFound
}

// UnmarshalAllocationKey unmarshals an allocation key from a datastore key.
func (as *allocationStore) UnmarshalAllocationKey(key ds.Key) (*AllocationKey, error) {
	parts := key.Namespaces()
	lenparts := len(parts)
	// Expected format: /allocation/pool1/sub1/AQ
	const canonicalNsNmb = 4
	if lenparts < canonicalNsNmb {
		return nil, ErrMalformedKey
	}
	poolID := parts[lenparts-3]
	subscriberID := parts[lenparts-2]
	offsetBase64 := parts[lenparts-1]

	// Decode offset from base64
	offsetBytes, err := base64.RawURLEncoding.DecodeString(offsetBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode offset: %w", err)
	}

	return &AllocationKey{
		PoolID:       poolID,
		IPOffset:     new(big.Int).SetBytes(offsetBytes),
		SubscriberID: subscriberID,
	}, nil
}

// UnmarshalSubscriberKey unmarshals a subscriber key from a datastore key.
func (as *allocationStore) UnmarshalSubscriberKey(key ds.Key, subscriberID string) (*AllocationKey, error) {
	parts := key.Namespaces()
	lenparts := len(parts)
	// Expected format: /subscriber/sub1/pool1/AQ
	const canonicalNsNmb = 4
	if lenparts < canonicalNsNmb {
		return nil, ErrMalformedKey
	}
	poolID := parts[lenparts-2]
	offsetBase64 := parts[lenparts-1]

	// Decode offset from base64
	offsetBytes, err := base64.RawURLEncoding.DecodeString(offsetBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode offset: %w", err)
	}

	return &AllocationKey{
		PoolID:       poolID,
		IPOffset:     new(big.Int).SetBytes(offsetBytes),
		SubscriberID: subscriberID,
	}, nil
}

// ListAllocationsByPool lists all allocations for a pool.
func (as *allocationStore) ListAllocationsByPool(ctx context.Context, poolID string) ([]*Allocation, error) {
	p, err := as.poolStore.GetPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("getting pool: %w", err)
	}

	q := query.Query{Prefix: as.poolAllocationPrefix.ChildString(poolID).String()}
	results, err := as.store.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to query allocator state: %w", err)
	}
	defer results.Close()

	var allocs []*Allocation
	for result := range results.Next() {
		if result.Value == nil {
			continue
		}
		key := ds.RawKey(result.Key)
		ar, err := as.UnmarshalAllocationKey(key)
		if err != nil {
			if errors.Is(err, ErrMalformedKey) {
				continue
			}
			return nil, fmt.Errorf("failed to unmarshal allocation key: %w", err)
		}
		a, err := ar.GetAllocation(p, result.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to get allocation: %w", err)
		}

		allocs = append(allocs, a)
	}
	return allocs, nil
}

// CountAllocationsByPool counts allocations for a pool.
func (as *allocationStore) CountAllocationsByPool(ctx context.Context, poolID string) (int, error) {
	q := query.Query{Prefix: as.poolAllocationPrefix.ChildString(poolID).String(), KeysOnly: true}
	results, err := as.store.Query(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("failed to query allocator state: %w", err)
	}
	defer results.Close()

	count := 0
	for range results.Next() {
		count++
	}
	return count, nil
}
