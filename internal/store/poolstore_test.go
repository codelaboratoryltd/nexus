package store

import (
	"context"
	"net"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/sync"
)

func TestNewPoolStore(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")

	store := NewPoolStore(datastore, prefix)
	if store == nil {
		t.Fatal("NewPoolStore returned nil")
	}
}

func TestPoolStore_SaveAndGetPool(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &Pool{
		ID:       "pool1",
		CIDR:     *cidr,
		Prefix:   28,
		Metadata: map[string]string{"env": "test"},
	}

	// Save pool
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Get pool
	retrieved, err := store.GetPool(ctx, "pool1")
	if err != nil {
		t.Fatalf("GetPool() error = %v", err)
	}

	if retrieved.ID != pool.ID {
		t.Errorf("GetPool() ID = %s, want %s", retrieved.ID, pool.ID)
	}
	if retrieved.Prefix != pool.Prefix {
		t.Errorf("GetPool() Prefix = %d, want %d", retrieved.Prefix, pool.Prefix)
	}
	if retrieved.CIDR.String() != pool.CIDR.String() {
		t.Errorf("GetPool() CIDR = %s, want %s", retrieved.CIDR.String(), pool.CIDR.String())
	}
}

func TestPoolStore_GetPool_NotFound(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, err := store.GetPool(ctx, "nonexistent")
	if err == nil {
		t.Fatal("GetPool() should return error for non-existent pool")
	}
}

func TestPoolStore_DeletePool(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &Pool{
		ID:     "pool1",
		CIDR:   *cidr,
		Prefix: 28,
	}

	// Save pool
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Delete pool
	if err := store.DeletePool(ctx, "pool1"); err != nil {
		t.Fatalf("DeletePool() error = %v", err)
	}

	// Verify pool is deleted
	_, err := store.GetPool(ctx, "pool1")
	if err == nil {
		t.Error("GetPool() should return error after deletion")
	}
}

func TestPoolStore_ListPools(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	// Initially empty
	pools, err := store.ListPools(ctx)
	if err != nil {
		t.Fatalf("ListPools() error = %v", err)
	}
	if len(pools) != 0 {
		t.Errorf("ListPools() returned %d pools, want 0", len(pools))
	}

	// Add pools
	_, cidr1, _ := net.ParseCIDR("192.168.0.0/24")
	_, cidr2, _ := net.ParseCIDR("10.0.0.0/16")

	pool1 := &Pool{ID: "pool1", CIDR: *cidr1, Prefix: 28}
	pool2 := &Pool{ID: "pool2", CIDR: *cidr2, Prefix: 24}

	if err := store.SavePool(ctx, pool1); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}
	if err := store.SavePool(ctx, pool2); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// List pools
	pools, err = store.ListPools(ctx)
	if err != nil {
		t.Fatalf("ListPools() error = %v", err)
	}
	if len(pools) != 2 {
		t.Errorf("ListPools() returned %d pools, want 2", len(pools))
	}
}

func TestPoolStore_UnmarshalKey(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix).(*poolStore)

	// Test with valid key but nil value
	key := ds.NewKey("/pool/pool1")
	pool, err := store.UnmarshalKey(key, nil)
	if err != nil {
		t.Errorf("UnmarshalKey() for nil value error = %v", err)
	}
	if pool.ID != "pool1" {
		t.Errorf("UnmarshalKey() ID = %s, want pool1", pool.ID)
	}

	// Test with a key that has namespaces
	key = ds.NewKey("/pool/testpool")
	pool, err = store.UnmarshalKey(key, nil)
	if err != nil {
		t.Errorf("UnmarshalKey() error = %v", err)
	}
	if pool.ID != "testpool" {
		t.Errorf("UnmarshalKey() ID = %s, want testpool", pool.ID)
	}
}

func TestPoolStore_UpdatePool(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &Pool{
		ID:       "pool1",
		CIDR:     *cidr,
		Prefix:   28,
		Metadata: map[string]string{"env": "test"},
	}

	// Save pool
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Update pool
	pool.Prefix = 30
	pool.Metadata["env"] = "prod"
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() for update error = %v", err)
	}

	// Verify update
	retrieved, err := store.GetPool(ctx, "pool1")
	if err != nil {
		t.Fatalf("GetPool() error = %v", err)
	}
	if retrieved.Prefix != 30 {
		t.Errorf("GetPool() Prefix = %d, want 30", retrieved.Prefix)
	}
	if retrieved.Metadata["env"] != "prod" {
		t.Errorf("GetPool() Metadata[env] = %s, want prod", retrieved.Metadata["env"])
	}
}

func TestPoolStore_PoolWithExclusions(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, cidr, _ := net.ParseCIDR("10.0.0.0/16")
	pool := &Pool{
		ID:         "pool1",
		CIDR:       *cidr,
		Prefix:     24,
		Exclusions: []string{"10.0.0.0/24", "10.0.1.0/24"},
	}

	// Save pool
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Get pool and verify exclusions
	retrieved, err := store.GetPool(ctx, "pool1")
	if err != nil {
		t.Fatalf("GetPool() error = %v", err)
	}
	if len(retrieved.Exclusions) != 2 {
		t.Errorf("GetPool() Exclusions length = %d, want 2", len(retrieved.Exclusions))
	}
}

func TestPoolStore_PoolWithShardingFactor(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	prefix := ds.NewKey("pool")
	store := NewPoolStore(datastore, prefix)
	ctx := context.Background()

	_, cidr, _ := net.ParseCIDR("172.16.0.0/12")
	pool := &Pool{
		ID:             "pool1",
		CIDR:           *cidr,
		Prefix:         24,
		ShardingFactor: 8,
	}

	// Save pool
	if err := store.SavePool(ctx, pool); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Get pool and verify sharding factor
	retrieved, err := store.GetPool(ctx, "pool1")
	if err != nil {
		t.Fatalf("GetPool() error = %v", err)
	}
	if retrieved.ShardingFactor != 8 {
		t.Errorf("GetPool() ShardingFactor = %d, want 8", retrieved.ShardingFactor)
	}
}
