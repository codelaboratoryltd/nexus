package store

import (
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/util"
)

// Pool represents a resource pool (e.g., IP address pool).
type Pool struct {
	ID             string            `json:"id" cbor:"id"`
	CIDR           net.IPNet         `json:"cidr" cbor:"cidr"`
	Prefix         int               `json:"prefix" cbor:"prefix"`
	Exclusions     []string          `json:"exclusions" cbor:"exclusions"`
	Metadata       map[string]string `json:"metadata" cbor:"metadata"`
	ShardingFactor int               `json:"sharding_factor" cbor:"sharding_factor"`
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

// Allocation represents an IP allocation for a subscriber.
type Allocation struct {
	PoolID       string    `json:"pool_id,omitempty" cbor:"pool_id"`
	IP           net.IP    `json:"ip,omitempty" cbor:"ip"`
	Timestamp    time.Time `json:"timestamp" cbor:"timestamp"`
	SubscriberID string    `json:"subscriber_id,omitempty" cbor:"subscriber_id"`
}

// AllocationKey represents the key components for an allocation.
type AllocationKey struct {
	PoolID       string
	IPOffset     *big.Int
	SubscriberID string
}

// GetAllocation reconstructs an Allocation from the key and value bytes.
func (a *AllocationKey) GetAllocation(p *Pool, keysValue []byte) (*Allocation, error) {
	var timestamp time.Time
	if keysValue != nil {
		timestamp = util.UnmarshalTime(keysValue)
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
		Timestamp:    timestamp,
	}, nil
}
