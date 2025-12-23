package hashring

import (
	"cmp"
	"container/list"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"

	"github.com/apparentlymart/go-cidr/cidr"
)

// HashRing represents the hash ring structure for distributed IP pools
type HashRing struct {
	mu        sync.Mutex
	hashIDs   map[HashID]struct{}
	ipPools   map[PoolID]IPPool
	subnets   map[HashID]map[PoolID]*net.IPNet
	hashOrder map[HashID]int
}

// NewHashRing initializes a new HashRing with no IP pools
func NewHashRing() *HashRing {
	return &HashRing{
		hashIDs:   make(map[HashID]struct{}),
		ipPools:   make(map[PoolID]IPPool),
		subnets:   make(map[HashID]map[PoolID]*net.IPNet),
		hashOrder: make(map[HashID]int),
	}
}

// RegisterPool registers a new IP pool with the HashRing
func (hr *HashRing) RegisterPool(pool IPPool) error {
	// Validate pool info first
	if pool.Network.IP == nil || pool.Network.Mask == nil || len(pool.Network.Mask) == 0 {
		return errInvalidPoolCIDR
	}

	hr.mu.Lock()
	defer hr.mu.Unlock()
	hr.ipPools[pool.ID] = pool
	return hr.updateHashRing()
}

// UnregisterPool removes an IP pool from the HashRing
func (hr *HashRing) UnregisterPool(id PoolID) error {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	delete(hr.ipPools, id)
	return hr.updateHashRing()
}

// AddHash registers a hash with the HashRing
func (hr *HashRing) AddHash(hashID HashID) error {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if _, exists := hr.hashIDs[hashID]; exists {
		return nil
	}

	hr.hashIDs[hashID] = struct{}{}
	return hr.updateHashRing()
}

// RemoveHash removes a hash from the hash ring
func (hr *HashRing) RemoveHash(hashID HashID) error {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if _, exists := hr.hashIDs[hashID]; !exists {
		return nil
	}
	delete(hr.hashIDs, hashID)
	return hr.updateHashRing()
}

// removePool is an internal function (no lock) to remove an IP pool and update the hash ring
func (hr *HashRing) removePool(id PoolID) error {
	delete(hr.ipPools, id)
	return hr.updateHashRing()
}

// updateHashRing updates the hashring, redistributing IP pools across hashIDs
func (hr *HashRing) updateHashRing() error {
	if len(hr.hashIDs) == 0 {
		hr.resetHashIDs()
		return nil
	}

	// Convert hashIDs map keys to a sorted slice for consistent distribution
	orderedHashIDs := sorted(hr.hashIDs)

	// Prepare new hashOrder map
	newHashOrder := makeHashOrder(orderedHashIDs)

	// Distribute subnets
	newSubnets, err := makeSubnetsDistribution(hr.ipPools, orderedHashIDs)
	var invalidPool InvalidPoolError
	if err != nil && errors.As(err, &invalidPool) {
		return hr.removePool(invalidPool.poolID)
	}
	if err != nil {
		return fmt.Errorf("cannot compute subnets distribution: %w", err)
	}

	// Update the state of the hashring with newly computed values
	hr.hashOrder = newHashOrder
	hr.subnets = newSubnets

	return nil
}

func (hr *HashRing) resetHashIDs() {
	hr.hashOrder = make(map[HashID]int)
	hr.subnets = make(map[HashID]map[PoolID]*net.IPNet)
	hr.hashIDs = make(map[HashID]struct{})
}

func makeHashOrder(orderedHashIDs []HashID) map[HashID]int {
	newHashOrder := make(map[HashID]int)
	for i, hashID := range orderedHashIDs {
		newHashOrder[hashID] = i
	}
	return newHashOrder
}

func sorted[E cmp.Ordered](values map[E]struct{}) []E {
	ret := make([]E, 0, len(values))
	for val := range values {
		ret = append(ret, val)
	}
	slices.Sort(ret)
	return ret
}

type subnetsDistribution map[HashID]map[PoolID]*net.IPNet

// makeSubnetsDistribution distributes the pool subnets from the ipPools
// over hashring hashes from orderedHashIDs slice
func makeSubnetsDistribution(
	ipPools map[PoolID]IPPool,
	orderedHashIDs []HashID,
) (subnetsDistribution, error) {
	newSubnets := make(map[HashID]map[PoolID]*net.IPNet)

	for poolID, pool := range ipPools {
		numCIDRSubnets := uint(len(orderedHashIDs))
		subnets, err := splitCIDR(pool.Network, numCIDRSubnets)
		if errors.Is(err, errEmptyCIDR) {
			return nil, InvalidPoolError{err: err, poolID: poolID}
		}
		if err != nil {
			return nil, fmt.Errorf("error splitting CIDR for pool %s: %w", poolID, err)
		}

		hashIndex := 0
		for i := 0; i < len(subnets); i++ {
			hashID := orderedHashIDs[hashIndex]
			if newSubnets[hashID] == nil {
				newSubnets[hashID] = make(map[PoolID]*net.IPNet)
			}
			newSubnets[hashID][poolID] = subnets[i]
			hashIndex = (hashIndex + 1) % len(orderedHashIDs)
		}
	}

	return newSubnets, nil
}

// GetHashIPNets returns the IP subnets assigned to a given hash ID
func (hr *HashRing) GetHashIPNets(hashID HashID) (map[PoolID]*net.IPNet, error) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if _, exists := hr.hashIDs[hashID]; !exists {
		return nil, &HashNotFoundError{HashID: hashID}
	}

	if len(hr.subnets) == 0 {
		return map[PoolID]*net.IPNet{}, nil
	}
	subnets, ok := hr.subnets[hashID]
	if !ok {
		return nil, &HashNotFoundError{HashID: hashID}
	}
	return subnets, nil
}

func splitCIDR(network *net.IPNet, parts uint) ([]*net.IPNet, error) {
	if network.IP == nil {
		return nil, fmt.Errorf("invalid CIDR IP - empty IP - %w", errEmptyCIDR)
	}
	if len(network.Mask) == 0 {
		return nil, fmt.Errorf("invalid CIDR mask - empty mask - %w", errEmptyCIDR)
	}

	subnetsList := list.New()
	subnetsList.PushBack(network)

	for subnetsList.Len() > 0 && uint(subnetsList.Len()) < parts {
		subnet, subnetOK := subnetsList.Remove(subnetsList.Front()).(*net.IPNet)
		if !subnetOK {
			return nil, fmt.Errorf("error splitting CIDR %w", errInvalidSubnetType)
		}

		loSubnet, err := cidr.Subnet(subnet, 1, 0)
		if err != nil {
			return nil, fmt.Errorf("error splitting CIDR %s: %w", subnet.String(), err)
		}

		hiSubnet, err := cidr.Subnet(subnet, 1, 1)
		if err != nil {
			return nil, fmt.Errorf("error splitting CIDR %s: %w", subnet.String(), err)
		}

		subnetsList.PushBack(loSubnet)
		subnetsList.PushBack(hiSubnet)
	}
	if subnetsList.Len() > 0 && uint(subnetsList.Len()) != parts {
		return nil, fmt.Errorf("%w; addr = %s, parts = %d", errCIDRNotSplitable, network.String(), parts)
	}

	ret := make([]*net.IPNet, 0, subnetsList.Len())
	for e := subnetsList.Front(); e != nil; e = e.Next() {
		subnet, subnetOK := e.Value.(*net.IPNet)
		if !subnetOK {
			return nil, fmt.Errorf("error splitting CIDR %w", errInvalidSubnetType)
		}
		ret = append(ret, subnet)
	}

	return ret, nil
}

// GetHashOrder returns the position (order) of the given hash in the precomputed order map
func (hr *HashRing) GetHashOrder(hashID HashID) (int, error) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	order, exists := hr.hashOrder[hashID]
	if !exists {
		return -1, &HashNotFoundError{HashID: hashID}
	}
	return order, nil
}

// GetHashOrders returns all hash orders
func (hr *HashRing) GetHashOrders() map[HashID]int {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	order := make(map[HashID]int)
	for hashID, hashIDOrder := range hr.hashOrder {
		order[hashID] = hashIDOrder
	}
	return order
}

// GetHashIDs returns all hash IDs (for testing)
func (hr *HashRing) GetHashIDs() map[HashID]struct{} {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	return hr.hashIDs
}

// GetIPPools returns all IP pools (for testing)
func (hr *HashRing) GetIPPools() []IPPool {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	ret := make([]IPPool, 0, len(hr.ipPools))
	for _, pool := range hr.ipPools {
		ret = append(ret, pool)
	}
	return ret
}
