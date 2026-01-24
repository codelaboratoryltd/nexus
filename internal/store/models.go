package store

import (
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/util"
	"github.com/fxamacker/cbor/v2"
)

// Pool represents a resource pool (e.g., IP address pool).
type Pool struct {
	ID             string            `json:"id" cbor:"id"`
	CIDR           net.IPNet         `json:"cidr" cbor:"cidr"`
	Prefix         int               `json:"prefix" cbor:"prefix"`
	Exclusions     []string          `json:"exclusions" cbor:"exclusions"`
	Metadata       map[string]string `json:"metadata" cbor:"metadata"`
	ShardingFactor int               `json:"sharding_factor" cbor:"sharding_factor"`
	BackupRatio    float64           `json:"backup_ratio" cbor:"backup_ratio"` // 0.0-1.0, percentage of pool reserved for backup allocations
}

// Validate checks if the pool configuration is valid.
func (p *Pool) Validate() error {
	// Check if the CIDR is legit.
	if p.CIDR.IP == nil || p.CIDR.Mask == nil || len(p.CIDR.Mask) == 0 {
		return ErrInvalidPoolCIDR
	}

	// Check if the prefix is legit.
	maskSize, bits := p.CIDR.Mask.Size()
	delegatedPrefixLength := p.Prefix
	if delegatedPrefixLength < maskSize || delegatedPrefixLength > bits {
		return ErrInvalidPoolPrefix
	}

	return nil
}

// Node represents a node in the cluster (e.g., OLT-BNG).
type Node struct {
	ID         string            `json:"id" cbor:"id"`
	BestBefore time.Time         `json:"best_before" cbor:"best_before"` // The time after which this node is considered lost
	Metadata   map[string]string `json:"metadata" cbor:"metadata"`
}

// AllocationType defines the type of allocation (for TTL behavior).
type AllocationType string

const (
	// AllocationTypeSession is a session-based allocation that expires after TTL.
	AllocationTypeSession AllocationType = "session"
	// AllocationTypeSticky is a sticky allocation that can be renewed.
	AllocationTypeSticky AllocationType = "sticky"
	// AllocationTypePermanent is a permanent allocation with no expiration.
	AllocationTypePermanent AllocationType = "permanent"
)

// Allocation represents an IP allocation for a subscriber.
type Allocation struct {
	PoolID       string    `json:"pool_id,omitempty" cbor:"pool_id"`
	IP           net.IP    `json:"ip,omitempty" cbor:"ip"`
	Timestamp    time.Time `json:"timestamp" cbor:"timestamp"`
	SubscriberID string    `json:"subscriber_id,omitempty" cbor:"subscriber_id"`
	NodeID       string    `json:"node_id,omitempty" cbor:"node_id"`               // Primary node that owns this allocation
	BackupNodeID string    `json:"backup_node_id,omitempty" cbor:"backup_node_id"` // Standby node that has cached copy
	IsBackup     bool      `json:"is_backup,omitempty" cbor:"is_backup"`           // True if this is a backup allocation

	// TTL fields (Demo F - WiFi mode)
	// Uses epoch-based expiration for efficiency: allocations expire when Epoch < currentEpoch - gracePeriod
	TTL         int64          `json:"ttl,omitempty" cbor:"ttl"`                   // TTL in epochs (0 = permanent)
	Epoch       uint64         `json:"epoch,omitempty" cbor:"epoch"`               // Epoch when allocation was created/renewed
	ExpiresAt   time.Time      `json:"expires_at,omitempty" cbor:"expires_at"`     // Approximate expiration (informational)
	LastRenewed time.Time      `json:"last_renewed,omitempty" cbor:"last_renewed"` // Last renewal timestamp
	AllocType   AllocationType `json:"alloc_type,omitempty" cbor:"alloc_type"`     // Allocation type
}

// AllocationKey represents the key components for an allocation.
type AllocationKey struct {
	PoolID       string
	IPOffset     *big.Int
	SubscriberID string
}

// AllocationValue represents the stored value for an allocation.
// This extends the legacy timestamp-only format with node backup information and TTL.
type AllocationValue struct {
	Timestamp    time.Time `cbor:"timestamp"`
	NodeID       string    `cbor:"node_id,omitempty"`
	BackupNodeID string    `cbor:"backup_node_id,omitempty"`
	IsBackup     bool      `cbor:"is_backup,omitempty"`

	// TTL fields (Demo F - WiFi mode)
	// Uses epoch-based expiration: allocations expire when Epoch < currentEpoch - gracePeriod
	TTL         int64          `cbor:"ttl,omitempty"`
	Epoch       uint64         `cbor:"epoch,omitempty"`
	ExpiresAt   time.Time      `cbor:"expires_at,omitempty"`
	LastRenewed time.Time      `cbor:"last_renewed,omitempty"`
	AllocType   AllocationType `cbor:"alloc_type,omitempty"`
}

// GetAllocation reconstructs an Allocation from the key and value bytes.
func (a *AllocationKey) GetAllocation(p *Pool, keysValue []byte) (*Allocation, error) {
	var allocValue AllocationValue

	if keysValue != nil {
		// Try to unmarshal as CBOR first (new format)
		if err := cbor.Unmarshal(keysValue, &allocValue); err != nil {
			// Fall back to legacy timestamp-only format (8 bytes)
			if len(keysValue) == 8 {
				allocValue.Timestamp = util.UnmarshalTime(keysValue)
			}
			// If neither works, leave allocValue with zero values
		}
	}

	// Calculate IP from offset
	ip, err := util.OffsetToIP(&p.CIDR, a.IPOffset.Sub(a.IPOffset, big.NewInt(1)))
	if err != nil {
		return nil, fmt.Errorf("failed to calculate IP from offset: %w", err)
	}

	return &Allocation{
		PoolID:       a.PoolID,
		SubscriberID: a.SubscriberID,
		IP:           ip,
		Timestamp:    allocValue.Timestamp,
		NodeID:       allocValue.NodeID,
		BackupNodeID: allocValue.BackupNodeID,
		IsBackup:     allocValue.IsBackup,
		TTL:          allocValue.TTL,
		Epoch:        allocValue.Epoch,
		ExpiresAt:    allocValue.ExpiresAt,
		LastRenewed:  allocValue.LastRenewed,
		AllocType:    allocValue.AllocType,
	}, nil
}
