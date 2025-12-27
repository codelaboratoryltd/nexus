package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// poolStore manages pool data with datastore backend.
type poolStore struct {
	ds     ds.Datastore
	prefix ds.Key
}

// NewPoolStore creates a new pool store with the given datastore and prefix.
func NewPoolStore(datastore ds.Datastore, prefix ds.Key) PoolStore {
	return &poolStore{
		ds:     datastore,
		prefix: prefix,
	}
}

// UnmarshalKey deserializes a pool from a datastore key and value.
func (ps *poolStore) UnmarshalKey(key ds.Key, value []byte) (*Pool, error) {
	parts := key.Namespaces()
	partsLen := len(parts)
	if partsLen < 1 {
		return nil, ErrMalformedKey
	}
	result := &Pool{
		ID: parts[partsLen-1],
	}
	if value != nil {
		if err := cbor.Unmarshal(value, result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pool data: %w", err)
		}
	}
	return result, nil
}

// putPool adds or updates a pool in the datastore.
func (ps *poolStore) putPool(ctx context.Context, pool *Pool) error {
	// Serialize pool data
	data, err := cbor.Marshal(pool)
	if err != nil {
		return fmt.Errorf("failed to marshal pool data: %w", err)
	}

	// Store the pool data using its ID as the key
	key := ps.prefix.ChildString(pool.ID)
	if err = ps.ds.Put(ctx, key, data); err != nil {
		return fmt.Errorf("failed to put pool data: %w", err)
	}
	return nil
}

// GetPool retrieves a pool by its ID.
func (ps *poolStore) GetPool(ctx context.Context, poolID string) (*Pool, error) {
	key := ps.prefix.ChildString(poolID)
	data, err := ps.ds.Get(ctx, key)
	if err != nil {
		if errors.Is(err, ds.ErrNotFound) {
			return nil, fmt.Errorf("pool not found: %w", err)
		}
		return nil, fmt.Errorf("failed to retrieve pool data: %w", err)
	}
	return ps.UnmarshalKey(key, data)
}

// SavePool saves a pool to the datastore.
func (ps *poolStore) SavePool(ctx context.Context, pool *Pool) error {
	return ps.putPool(ctx, pool)
}

// DeletePool removes a pool from the datastore.
func (ps *poolStore) DeletePool(ctx context.Context, id string) error {
	key := ps.prefix.ChildString(id)
	if err := ps.ds.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete pool data: %w", err)
	}
	return nil
}

// ListPools retrieves all pools from the datastore.
func (ps *poolStore) ListPools(ctx context.Context) ([]*Pool, error) {
	q := query.Query{Prefix: ps.prefix.String()}
	results, err := ps.ds.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to query pools: %w", err)
	}
	defer results.Close()

	pools := make([]*Pool, 0)
	for result := range results.Next() {
		if result.Value == nil {
			continue
		}
		key := ds.RawKey(result.Key)
		pool, err := ps.UnmarshalKey(key, result.Value)
		if err != nil {
			if errors.Is(err, ErrMalformedKey) {
				continue
			}
			continue
		}
		pools = append(pools, pool)
	}

	return pools, nil
}
