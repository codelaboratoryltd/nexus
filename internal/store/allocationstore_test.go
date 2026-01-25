package store

import (
	"context"
	"net"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/sync"
)

func setupAllocationStore(t *testing.T) (*allocationStore, *poolStore, context.Context) {
	t.Helper()
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	poolPrefix := ds.NewKey("/pool")
	allocationPrefix := ds.NewKey("/allocation")
	subscriberPrefix := ds.NewKey("/subscriber")

	poolStore := NewPoolStore(datastore, poolPrefix).(*poolStore)
	allocStore := NewAllocationStore(datastore, allocationPrefix, subscriberPrefix, poolStore)

	// Create a test pool
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	pool := &Pool{
		ID:     "test-pool",
		CIDR:   *cidr,
		Prefix: 28,
	}
	ctx := context.Background()
	if err := poolStore.SavePool(ctx, pool); err != nil {
		t.Fatalf("failed to save test pool: %v", err)
	}

	return allocStore, poolStore, ctx
}

func TestNewAllocationStore(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	poolPrefix := ds.NewKey("/pool")
	allocationPrefix := ds.NewKey("/allocation")
	subscriberPrefix := ds.NewKey("/subscriber")

	poolStore := NewPoolStore(datastore, poolPrefix)
	allocStore := NewAllocationStore(datastore, allocationPrefix, subscriberPrefix, poolStore)

	if allocStore == nil {
		t.Fatal("NewAllocationStore returned nil")
	}
}

func TestAllocationStore_SaveAndGetAllocation(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Get allocation
	retrieved, err := allocStore.GetAllocation(ctx, "test-pool", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation() error = %v", err)
	}

	if retrieved.SubscriberID != allocation.SubscriberID {
		t.Errorf("GetAllocation() SubscriberID = %s, want %s", retrieved.SubscriberID, allocation.SubscriberID)
	}
	if retrieved.IP.String() != allocation.IP.String() {
		t.Errorf("GetAllocation() IP = %s, want %s", retrieved.IP.String(), allocation.IP.String())
	}
}

func TestAllocationStore_GetAllocation_NotFound(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	_, err := allocStore.GetAllocation(ctx, "test-pool", "nonexistent")
	if err == nil {
		t.Fatal("GetAllocation() should return error for non-existent allocation")
	}
}

func TestAllocationStore_RemoveAllocation(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Remove allocation
	if err := allocStore.RemoveAllocation(ctx, "test-pool", "sub1"); err != nil {
		t.Fatalf("RemoveAllocation() error = %v", err)
	}

	// Verify allocation is removed
	_, err := allocStore.GetAllocation(ctx, "test-pool", "sub1")
	if err == nil {
		t.Error("GetAllocation() should return error after removal")
	}
}

func TestAllocationStore_GetAllocationBySubscriber(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Get by subscriber ID
	retrieved, err := allocStore.GetAllocationBySubscriber(ctx, "sub1")
	if err != nil {
		t.Fatalf("GetAllocationBySubscriber() error = %v", err)
	}

	if retrieved.PoolID != allocation.PoolID {
		t.Errorf("GetAllocationBySubscriber() PoolID = %s, want %s", retrieved.PoolID, allocation.PoolID)
	}
}

func TestAllocationStore_GetAllocationBySubscriber_NotFound(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	_, err := allocStore.GetAllocationBySubscriber(ctx, "nonexistent")
	if err == nil {
		t.Fatal("GetAllocationBySubscriber() should return error for non-existent subscriber")
	}
}

func TestAllocationStore_ListAllocationsByPool(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// Initially empty
	allocs, err := allocStore.ListAllocationsByPool(ctx, "test-pool")
	if err != nil {
		t.Fatalf("ListAllocationsByPool() error = %v", err)
	}
	if len(allocs) != 0 {
		t.Errorf("ListAllocationsByPool() returned %d allocations, want 0", len(allocs))
	}

	// Add allocations
	allocation1 := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}
	allocation2 := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub2",
		IP:           net.ParseIP("192.168.0.2"),
		Timestamp:    time.Now(),
	}

	if err := allocStore.SaveAllocation(ctx, allocation1); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}
	if err := allocStore.SaveAllocation(ctx, allocation2); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// List allocations
	allocs, err = allocStore.ListAllocationsByPool(ctx, "test-pool")
	if err != nil {
		t.Fatalf("ListAllocationsByPool() error = %v", err)
	}
	if len(allocs) != 2 {
		t.Errorf("ListAllocationsByPool() returned %d allocations, want 2", len(allocs))
	}
}

func TestAllocationStore_CountAllocationsByPool(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// Initially empty
	count, err := allocStore.CountAllocationsByPool(ctx, "test-pool")
	if err != nil {
		t.Fatalf("CountAllocationsByPool() error = %v", err)
	}
	if count != 0 {
		t.Errorf("CountAllocationsByPool() = %d, want 0", count)
	}

	// Add allocations
	for i := 1; i <= 5; i++ {
		allocation := &Allocation{
			PoolID:       "test-pool",
			SubscriberID: "sub" + string(rune('0'+i)),
			IP:           net.ParseIP("192.168.0." + string(rune('0'+i))),
			Timestamp:    time.Now(),
		}
		if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
			t.Fatalf("SaveAllocation() error = %v", err)
		}
	}

	// Count allocations
	count, err = allocStore.CountAllocationsByPool(ctx, "test-pool")
	if err != nil {
		t.Fatalf("CountAllocationsByPool() error = %v", err)
	}
	if count != 5 {
		t.Errorf("CountAllocationsByPool() = %d, want 5", count)
	}
}

func TestAllocationStore_AllocationWithNodeID(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}

	// Save allocation with node ID
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Get allocation and verify node ID
	retrieved, err := allocStore.GetAllocation(ctx, "test-pool", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation() error = %v", err)
	}
	if retrieved.NodeID != "bng-node-1" {
		t.Errorf("GetAllocation() NodeID = %s, want bng-node-1", retrieved.NodeID)
	}
}

func TestAllocationStore_ListAllocationsByNode(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// Add allocations for different nodes
	allocation1 := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}
	allocation2 := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub2",
		IP:           net.ParseIP("192.168.0.2"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}
	allocation3 := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub3",
		IP:           net.ParseIP("192.168.0.3"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-2",
	}

	if err := allocStore.SaveAllocation(ctx, allocation1); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}
	if err := allocStore.SaveAllocation(ctx, allocation2); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}
	if err := allocStore.SaveAllocation(ctx, allocation3); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// List allocations for node 1
	allocs, err := allocStore.ListAllocationsByNode(ctx, "bng-node-1")
	if err != nil {
		t.Fatalf("ListAllocationsByNode() error = %v", err)
	}
	if len(allocs) != 2 {
		t.Errorf("ListAllocationsByNode(bng-node-1) = %d allocations, want 2", len(allocs))
	}

	// List allocations for node 2
	allocs, err = allocStore.ListAllocationsByNode(ctx, "bng-node-2")
	if err != nil {
		t.Fatalf("ListAllocationsByNode() error = %v", err)
	}
	if len(allocs) != 1 {
		t.Errorf("ListAllocationsByNode(bng-node-2) = %d allocations, want 1", len(allocs))
	}
}

func TestAllocationStore_AssignBackupNode(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Assign backup node
	if err := allocStore.AssignBackupNode(ctx, "test-pool", "sub1", "bng-node-2"); err != nil {
		t.Fatalf("AssignBackupNode() error = %v", err)
	}

	// Verify backup node assignment
	retrieved, err := allocStore.GetAllocation(ctx, "test-pool", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation() error = %v", err)
	}
	if retrieved.BackupNodeID != "bng-node-2" {
		t.Errorf("GetAllocation() BackupNodeID = %s, want bng-node-2", retrieved.BackupNodeID)
	}
}

func TestAllocationStore_ListBackupAllocationsByNode(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// Create allocation with backup node
	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		NodeID:       "bng-node-1",
		BackupNodeID: "bng-node-2",
	}

	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// List backup allocations
	allocs, err := allocStore.ListBackupAllocationsByNode(ctx, "bng-node-2")
	if err != nil {
		t.Fatalf("ListBackupAllocationsByNode() error = %v", err)
	}
	if len(allocs) != 1 {
		t.Errorf("ListBackupAllocationsByNode() = %d allocations, want 1", len(allocs))
	}
}

func TestAllocationStore_EpochManagement(t *testing.T) {
	allocStore, _, _ := setupAllocationStore(t)

	// Test GetCurrentEpoch
	epoch := allocStore.GetCurrentEpoch()
	if epoch != 1 {
		t.Errorf("GetCurrentEpoch() = %d, want 1", epoch)
	}

	// Test AdvanceEpoch
	newEpoch := allocStore.AdvanceEpoch()
	if newEpoch != 2 {
		t.Errorf("AdvanceEpoch() = %d, want 2", newEpoch)
	}

	epoch = allocStore.GetCurrentEpoch()
	if epoch != 2 {
		t.Errorf("GetCurrentEpoch() after advance = %d, want 2", epoch)
	}
}

func TestAllocationStore_EpochPeriod(t *testing.T) {
	allocStore, _, _ := setupAllocationStore(t)

	// Test default epoch period
	period := allocStore.GetEpochPeriod()
	if period != time.Hour {
		t.Errorf("GetEpochPeriod() = %v, want %v", period, time.Hour)
	}

	// Test SetEpochPeriod
	allocStore.SetEpochPeriod(30 * time.Minute)
	period = allocStore.GetEpochPeriod()
	if period != 30*time.Minute {
		t.Errorf("GetEpochPeriod() after set = %v, want %v", period, 30*time.Minute)
	}
}

func TestAllocationStore_GracePeriod(t *testing.T) {
	allocStore, _, _ := setupAllocationStore(t)

	// Test default grace period
	grace := allocStore.GetGracePeriod()
	if grace != 2 {
		t.Errorf("GetGracePeriod() = %d, want 2", grace)
	}

	// Test SetGracePeriod
	allocStore.SetGracePeriod(5)
	grace = allocStore.GetGracePeriod()
	if grace != 5 {
		t.Errorf("GetGracePeriod() after set = %d, want 5", grace)
	}
}

func TestAllocationStore_AllocationWithTTL(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	now := time.Now()
	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    now,
		TTL:          3600,
		Epoch:        allocStore.GetCurrentEpoch(),
		LastRenewed:  now,
		AllocType:    AllocationTypeSession,
	}

	// Save allocation with TTL
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Get allocation and verify TTL fields
	retrieved, err := allocStore.GetAllocation(ctx, "test-pool", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation() error = %v", err)
	}
	if retrieved.TTL != 3600 {
		t.Errorf("GetAllocation() TTL = %d, want 3600", retrieved.TTL)
	}
	if retrieved.AllocType != AllocationTypeSession {
		t.Errorf("GetAllocation() AllocType = %s, want %s", retrieved.AllocType, AllocationTypeSession)
	}
}

func TestAllocationStore_RenewAllocation(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	now := time.Now()
	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    now,
		TTL:          3600,
		Epoch:        1,
		LastRenewed:  now,
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Advance epoch
	allocStore.AdvanceEpoch()

	// Renew allocation
	renewed, err := allocStore.RenewAllocation(ctx, "test-pool", "sub1", 0)
	if err != nil {
		t.Fatalf("RenewAllocation() error = %v", err)
	}

	if renewed.Epoch != allocStore.GetCurrentEpoch() {
		t.Errorf("RenewAllocation() Epoch = %d, want %d", renewed.Epoch, allocStore.GetCurrentEpoch())
	}
	if renewed.TTL != 3600 {
		t.Errorf("RenewAllocation() TTL = %d, want 3600 (original)", renewed.TTL)
	}
}

func TestAllocationStore_RenewAllocation_WithNewTTL(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	now := time.Now()
	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    now,
		TTL:          3600,
		Epoch:        1,
		LastRenewed:  now,
	}

	// Save allocation
	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Renew allocation with new TTL
	renewed, err := allocStore.RenewAllocation(ctx, "test-pool", "sub1", 7200)
	if err != nil {
		t.Fatalf("RenewAllocation() error = %v", err)
	}

	if renewed.TTL != 7200 {
		t.Errorf("RenewAllocation() TTL = %d, want 7200 (new)", renewed.TTL)
	}
}

func TestAllocationStore_RenewAllocation_NotFound(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	_, err := allocStore.RenewAllocation(ctx, "test-pool", "nonexistent", 0)
	if err == nil {
		t.Fatal("RenewAllocation() should return error for non-existent allocation")
	}
}

func TestAllocationStore_RenewAllocation_NoTTL(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// Create allocation without TTL (permanent)
	allocation := &Allocation{
		PoolID:       "test-pool",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
		TTL:          0, // Permanent
	}

	if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Renew should fail for permanent allocation without providing new TTL
	_, err := allocStore.RenewAllocation(ctx, "test-pool", "sub1", 0)
	if err == nil {
		t.Fatal("RenewAllocation() should return error for permanent allocation without new TTL")
	}
}

func TestAllocationStore_CleanupExpiredAllocations_NoExpired(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	// With only 1 epoch, nothing should be expired (need > gracePeriod epochs)
	deleted, err := allocStore.CleanupExpiredAllocations(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredAllocations() error = %v", err)
	}
	if deleted != 0 {
		t.Errorf("CleanupExpiredAllocations() = %d, want 0", deleted)
	}
}

func TestAllocationStore_UnmarshalAllocationKey(t *testing.T) {
	allocStore, _, _ := setupAllocationStore(t)

	tests := []struct {
		name       string
		key        ds.Key
		wantPoolID string
		wantSubID  string
		wantErr    bool
	}{
		{
			name:       "valid key",
			key:        ds.NewKey("/allocation/pool1/sub1/AQ"),
			wantPoolID: "pool1",
			wantSubID:  "sub1",
			wantErr:    false,
		},
		{
			name:    "too short key",
			key:     ds.NewKey("/allocation/pool1"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ak, err := allocStore.UnmarshalAllocationKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalAllocationKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ak.PoolID != tt.wantPoolID {
					t.Errorf("UnmarshalAllocationKey() PoolID = %s, want %s", ak.PoolID, tt.wantPoolID)
				}
				if ak.SubscriberID != tt.wantSubID {
					t.Errorf("UnmarshalAllocationKey() SubscriberID = %s, want %s", ak.SubscriberID, tt.wantSubID)
				}
			}
		})
	}
}

func TestAllocationStore_UnmarshalSubscriberKey(t *testing.T) {
	allocStore, _, _ := setupAllocationStore(t)

	tests := []struct {
		name         string
		key          ds.Key
		subscriberID string
		wantPoolID   string
		wantErr      bool
	}{
		{
			name:         "valid key",
			key:          ds.NewKey("/subscriber/sub1/pool1/AQ"),
			subscriberID: "sub1",
			wantPoolID:   "pool1",
			wantErr:      false,
		},
		{
			name:         "too short key",
			key:          ds.NewKey("/subscriber/sub1"),
			subscriberID: "sub1",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ak, err := allocStore.UnmarshalSubscriberKey(tt.key, tt.subscriberID)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalSubscriberKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ak.PoolID != tt.wantPoolID {
					t.Errorf("UnmarshalSubscriberKey() PoolID = %s, want %s", ak.PoolID, tt.wantPoolID)
				}
				if ak.SubscriberID != tt.subscriberID {
					t.Errorf("UnmarshalSubscriberKey() SubscriberID = %s, want %s", ak.SubscriberID, tt.subscriberID)
				}
			}
		})
	}
}

func TestAllocationStore_MultipleAllocationsForSameSubscriberDifferentPools(t *testing.T) {
	datastore := sync.MutexWrap(ds.NewMapDatastore())
	poolPrefix := ds.NewKey("/pool")
	allocationPrefix := ds.NewKey("/allocation")
	subscriberPrefix := ds.NewKey("/subscriber")

	poolStore := NewPoolStore(datastore, poolPrefix).(*poolStore)
	allocStore := NewAllocationStore(datastore, allocationPrefix, subscriberPrefix, poolStore)
	ctx := context.Background()

	// Create two pools
	_, cidr1, _ := net.ParseCIDR("192.168.0.0/24")
	_, cidr2, _ := net.ParseCIDR("10.0.0.0/24")

	pool1 := &Pool{ID: "pool1", CIDR: *cidr1, Prefix: 28}
	pool2 := &Pool{ID: "pool2", CIDR: *cidr2, Prefix: 28}

	if err := poolStore.SavePool(ctx, pool1); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}
	if err := poolStore.SavePool(ctx, pool2); err != nil {
		t.Fatalf("SavePool() error = %v", err)
	}

	// Create allocations in different pools for same subscriber
	allocation1 := &Allocation{
		PoolID:       "pool1",
		SubscriberID: "sub1",
		IP:           net.ParseIP("192.168.0.1"),
		Timestamp:    time.Now(),
	}
	allocation2 := &Allocation{
		PoolID:       "pool2",
		SubscriberID: "sub1",
		IP:           net.ParseIP("10.0.0.1"),
		Timestamp:    time.Now(),
	}

	if err := allocStore.SaveAllocation(ctx, allocation1); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}
	if err := allocStore.SaveAllocation(ctx, allocation2); err != nil {
		t.Fatalf("SaveAllocation() error = %v", err)
	}

	// Get allocations from each pool
	retrieved1, err := allocStore.GetAllocation(ctx, "pool1", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation(pool1) error = %v", err)
	}
	if retrieved1.IP.String() != "192.168.0.1" {
		t.Errorf("GetAllocation(pool1) IP = %s, want 192.168.0.1", retrieved1.IP.String())
	}

	retrieved2, err := allocStore.GetAllocation(ctx, "pool2", "sub1")
	if err != nil {
		t.Fatalf("GetAllocation(pool2) error = %v", err)
	}
	if retrieved2.IP.String() != "10.0.0.1" {
		t.Errorf("GetAllocation(pool2) IP = %s, want 10.0.0.1", retrieved2.IP.String())
	}
}

func TestAllocationStore_AllocationTypes(t *testing.T) {
	allocStore, _, ctx := setupAllocationStore(t)

	tests := []struct {
		name      string
		allocType AllocationType
	}{
		{"session allocation", AllocationTypeSession},
		{"sticky allocation", AllocationTypeSticky},
		{"permanent allocation", AllocationTypePermanent},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocation := &Allocation{
				PoolID:       "test-pool",
				SubscriberID: "sub" + string(rune('a'+i)),
				IP:           net.ParseIP("192.168.0." + string(rune('1'+i))),
				Timestamp:    time.Now(),
				AllocType:    tt.allocType,
			}

			if err := allocStore.SaveAllocation(ctx, allocation); err != nil {
				t.Fatalf("SaveAllocation() error = %v", err)
			}

			retrieved, err := allocStore.GetAllocation(ctx, allocation.PoolID, allocation.SubscriberID)
			if err != nil {
				t.Fatalf("GetAllocation() error = %v", err)
			}

			if retrieved.AllocType != tt.allocType {
				t.Errorf("GetAllocation() AllocType = %s, want %s", retrieved.AllocType, tt.allocType)
			}
		})
	}
}
