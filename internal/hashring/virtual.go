package hashring

import (
	"fmt"
	"net"
	"sync"
)

// VirtualHashRing is a hashring with virtual nodes for consistent hashing
type VirtualHashRing struct {
	mu                  sync.RWMutex
	nodes               map[NodeID]struct{}
	virtualNodes        map[VirtualNodeID]struct{}
	poolsHashRings      map[PoolID]*HashRing
	virtualNodesMapping HashMapping
}

// NewVirtualNodesHashRing creates a new virtual nodes hashring
func NewVirtualNodesHashRing() *VirtualHashRing {
	ring := &VirtualHashRing{
		nodes:               make(map[NodeID]struct{}),
		virtualNodes:        make(map[VirtualNodeID]struct{}),
		poolsHashRings:      make(map[PoolID]*HashRing),
		virtualNodesMapping: NewVirtualHashMapping(),
	}
	return ring
}

// ListNodes returns all node IDs
func (r *VirtualHashRing) ListNodes() []NodeID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return keys(r.nodes)
}

// ListVirtualNodes returns all virtual node IDs
func (r *VirtualHashRing) ListVirtualNodes() []VirtualNodeID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return keys(r.virtualNodes)
}

func keys[T comparable](m map[T]struct{}) []T {
	list := make([]T, 0, len(m))
	for key := range m {
		list = append(list, key)
	}
	return list
}

// GetHashMapping returns a copy of the current hash mapping
func (r *VirtualHashRing) GetHashMapping() HashMapping {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.virtualNodesMapping.Copy()
}

// AddNode adds a node to the hashring
func (r *VirtualHashRing) AddNode(nodeID NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[nodeID]; exists {
		return nil
	}

	r.nodes[nodeID] = struct{}{}
	r.virtualNodesMapping.AddNode(nodeID)
	r.updateVirtualHashRing()
	return nil
}

// RemoveNode removes a node from the hashring
func (r *VirtualHashRing) RemoveNode(nodeID NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[nodeID]; !exists {
		return nil
	}

	delete(r.nodes, nodeID)
	r.updateVirtualHashRing()
	return nil
}

func (r *VirtualHashRing) updateVirtualHashRing() {
	if len(r.nodes) == 0 {
		return
	}

	orderedNodes := sorted(r.nodes)

	virtualNodesPlacement := NewVirtualHashMapping()
	for nodeID := range r.nodes {
		virtualNodesPlacement.AddNode(nodeID)
	}

	// Use Rendezvous hashing for minimal VNode disruption on node changes.
	// When a node is removed, only vnodes assigned to that node are redistributed.
	// When a node is added, only ~1/N vnodes move to the new node.
	for poolID, poolHashRing := range r.poolsHashRings {
		for virtualNodeID, virtualNodeOrdinal := range poolHashRing.GetHashOrders() {
			// Use Rendezvous Hash to select node based on (vnode, nodes) combination
			selectedNode := RendezvousHash(uint64(virtualNodeOrdinal), orderedNodes)
			virtualNodesPlacement.Link(selectedNode, poolID, VirtualNodeID(virtualNodeID))
		}
	}

	r.virtualNodesMapping = virtualNodesPlacement
}

// RegisterPool adds a new pool to the hashring
func (r *VirtualHashRing) RegisterPool(pool IPPool) error {
	if pool.ID == "" {
		return fmt.Errorf("failed to register pool: %w", errEmptyPoolID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.poolsHashRings[pool.ID]; exists {
		return nil
	}

	r.poolsHashRings[pool.ID] = NewHashRing()

	vnodesCount := DefaultVNodesCount
	if pool.VNodesCount != 0 {
		vnodesCount = pool.VNodesCount
	}

	for i := range vnodesCount {
		vhashID := HashID(fmt.Sprintf("vhash-%d", i))
		err := r.poolsHashRings[pool.ID].AddHash(vhashID)
		if err != nil {
			return fmt.Errorf("failed to add virtual hash %s to pool %s: %w", vhashID, pool.ID, err)
		}
	}

	err := r.poolsHashRings[pool.ID].RegisterPool(pool)
	if err != nil {
		return fmt.Errorf("failed to register pool in virtual hashring: %w", err)
	}

	r.updateVirtualHashRing()
	return nil
}

// UnregisterPool removes a pool from the hashring
func (r *VirtualHashRing) UnregisterPool(poolID PoolID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.poolsHashRings[poolID]; !exists {
		return nil
	}

	err := r.poolsHashRings[poolID].UnregisterPool(poolID)
	if err != nil {
		return fmt.Errorf("failed to unregister pool in virtual hashring: %w", err)
	}
	delete(r.poolsHashRings, poolID)

	r.updateVirtualHashRing()
	return nil
}

// GetIPPools returns all IP pools (for testing)
func (r *VirtualHashRing) GetIPPools() []IPPool {
	r.mu.Lock()
	defer r.mu.Unlock()

	ret := make([]IPPool, 0, len(r.poolsHashRings))
	for _, poolHashRing := range r.poolsHashRings {
		ipPool := poolHashRing.GetIPPools()
		ret = append(ret, ipPool...)
	}
	return ret
}

// ListPoolSubnetsAtNode returns the subnets grouped by pool assigned to given node
func (r *VirtualHashRing) ListPoolSubnetsAtNode(nodeID NodeID) (PoolSubnetsSliceMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, hashExist := r.nodes[nodeID]
	if !hashExist {
		return nil, &HashNotFoundError{HashID: HashID(nodeID)}
	}

	ret := make(PoolSubnetsSliceMap, len(r.poolsHashRings))

	for poolID, vhashes := range r.virtualNodesMapping.NodePoolVNode[nodeID] {
		for vhash := range vhashes {
			vhashSubnets, err := r.poolsHashRings[poolID].GetHashIPNets(HashID(vhash))
			if err != nil {
				return nil, fmt.Errorf("failed to get virtual hash subnets: %w", err)
			}
			for poolIDFromPoolHashring, subnet := range vhashSubnets {
				if poolIDFromPoolHashring == poolID {
					ret[poolID] = append(ret[poolID], subnet)
				}
			}
		}
	}

	return ret, nil
}

// HashMapping represents mapping between nodes and virtual nodes
type HashMapping struct {
	NodePoolVNode map[NodeID]map[PoolID]map[VirtualNodeID]struct{}
	PoolVNodeNode map[PoolID]map[VirtualNodeID]NodeID
}

// NewVirtualHashMapping creates a new hash mapping
func NewVirtualHashMapping() HashMapping {
	return HashMapping{
		NodePoolVNode: make(map[NodeID]map[PoolID]map[VirtualNodeID]struct{}),
		PoolVNodeNode: make(map[PoolID]map[VirtualNodeID]NodeID),
	}
}

// Copy returns a copied instance
func (m HashMapping) Copy() HashMapping {
	mimic := NewVirtualHashMapping()

	for hash, poolVirtualHashes := range m.NodePoolVNode {
		mimic.AddNode(hash)

		for poolID, virtualHashes := range poolVirtualHashes {
			for vhash := range virtualHashes {
				mimic.Link(hash, poolID, vhash)
			}
		}
	}

	return mimic
}

// Link creates a mapping between owning node and a virtual node
func (m HashMapping) Link(hash NodeID, pool PoolID, vhash VirtualNodeID) {
	if m.PoolVNodeNode != nil && m.PoolVNodeNode[pool] == nil {
		if oldHash, ok := m.PoolVNodeNode[pool][vhash]; ok {
			if oldHash != hash {
				m.Unlink(oldHash, pool, vhash)
			}
		}
	}

	if _, ok := m.NodePoolVNode[hash]; !ok {
		m.NodePoolVNode[hash] = make(map[PoolID]map[VirtualNodeID]struct{})
	}
	if _, ok := m.NodePoolVNode[hash][pool]; !ok {
		m.NodePoolVNode[hash][pool] = make(map[VirtualNodeID]struct{})
	}
	m.NodePoolVNode[hash][pool][vhash] = struct{}{}

	if _, ok := m.PoolVNodeNode[pool]; !ok {
		m.PoolVNodeNode[pool] = make(map[VirtualNodeID]NodeID)
	}
	if _, ok := m.PoolVNodeNode[pool][vhash]; !ok {
		m.PoolVNodeNode[pool][vhash] = ""
	}
	m.PoolVNodeNode[pool][vhash] = hash
}

// Unlink removes mapping between owner hash ID and virtual hash ID
func (m HashMapping) Unlink(hash NodeID, pool PoolID, vhash VirtualNodeID) {
	if _, ok := m.NodePoolVNode[hash]; !ok {
		return
	}
	if _, ok := m.PoolVNodeNode[pool]; !ok {
		return
	}
	if _, ok := m.NodePoolVNode[hash][pool]; !ok {
		return
	}
	delete(m.NodePoolVNode[hash], pool)
	delete(m.PoolVNodeNode[pool], vhash)
	if len(m.PoolVNodeNode[pool]) == 0 {
		delete(m.PoolVNodeNode, pool)
	}
}

// AddNode adds a node to the mapping
func (m HashMapping) AddNode(nodeID NodeID) {
	if _, ok := m.NodePoolVNode[nodeID]; !ok {
		m.NodePoolVNode[nodeID] = make(map[PoolID]map[VirtualNodeID]struct{})
	}
}

// HasNode checks if a node exists
func (m HashMapping) HasNode(nodeID NodeID) bool {
	_, ok := m.NodePoolVNode[nodeID]
	return ok
}

// HasVirtualHash checks if a virtual hash exists
func (m HashMapping) HasVirtualHash(pool PoolID, vhash VirtualNodeID) bool {
	_, ok := m.PoolVNodeNode[pool][vhash]
	return ok
}

// RemoveVirtualHash removes a virtual hash
func (m HashMapping) RemoveVirtualHash(pool PoolID, vhash VirtualNodeID) {
	if hash, ok := m.PoolVNodeNode[pool][vhash]; ok {
		m.Unlink(hash, pool, vhash)
	}
}

// ListOwnedVirtualHashes returns virtual hashes owned by a node
func (m HashMapping) ListOwnedVirtualHashes(nodeID NodeID) map[PoolID]map[VirtualNodeID]struct{} {
	if _, ok := m.NodePoolVNode[nodeID]; !ok {
		return nil
	}
	return m.NodePoolVNode[nodeID]
}

// AddPool is a convenience wrapper for RegisterPool
func (r *VirtualHashRing) AddPool(poolID string, network *net.IPNet) error {
	return r.RegisterPool(IPPool{
		ID:          PoolID(poolID),
		Network:     network,
		VNodesCount: DefaultVNodesCount,
	})
}

// RemovePool is a convenience wrapper for UnregisterPool
func (r *VirtualHashRing) RemovePool(poolID string) error {
	return r.UnregisterPool(PoolID(poolID))
}

// AllocateIP returns an IP for a subscriber from a pool using consistent hashing.
// The subscriber ID is hashed to deterministically select a virtual node,
// then an IP is allocated from that node's subnet.
func (r *VirtualHashRing) AllocateIP(poolID string, subscriberID string) net.IP {
	r.mu.RLock()
	defer r.mu.RUnlock()

	poolHashRing, exists := r.poolsHashRings[PoolID(poolID)]
	if !exists {
		return nil
	}

	// Use subscriber ID to determine which virtual node to use
	hash := hashString(subscriberID)
	hashOrders := poolHashRing.GetHashOrders()

	if len(hashOrders) == 0 {
		return nil
	}

	// Find the virtual node for this subscriber
	vnodeIndex := int(hash % uint64(len(hashOrders)))
	var selectedVNode HashID
	i := 0
	for vnode := range hashOrders {
		if i == vnodeIndex {
			selectedVNode = vnode
			break
		}
		i++
	}

	// Get the subnet for this virtual node
	subnets, err := poolHashRing.GetHashIPNets(selectedVNode)
	if err != nil {
		return nil
	}

	for _, subnet := range subnets {
		// Generate a deterministic IP from the subnet based on subscriber hash
		ip := ipFromSubnetAndHash(subnet, hash)
		if ip != nil {
			return ip
		}
	}

	return nil
}

// ipFromSubnetAndHash generates a deterministic IP within a subnet
func ipFromSubnetAndHash(subnet *net.IPNet, hash uint64) net.IP {
	if subnet == nil || subnet.IP == nil {
		return nil
	}

	ones, bits := subnet.Mask.Size()
	hostBits := bits - ones
	if hostBits <= 0 {
		return nil
	}

	// Calculate number of available IPs (excluding network and broadcast for IPv4)
	maxHosts := uint64(1) << uint(hostBits)
	if bits == 32 && hostBits > 1 { // IPv4 with room for network/broadcast
		maxHosts -= 2 // Exclude network and broadcast
	}

	if maxHosts == 0 {
		return nil
	}

	// Select host offset based on hash
	hostOffset := (hash % maxHosts)
	if bits == 32 && hostBits > 1 {
		hostOffset++ // Skip network address
	}

	// Calculate the IP
	ip := make(net.IP, len(subnet.IP))
	copy(ip, subnet.IP)

	// Add the offset to the IP
	for i := len(ip) - 1; i >= 0 && hostOffset > 0; i-- {
		sum := uint64(ip[i]) + (hostOffset & 0xFF)
		ip[i] = byte(sum)
		hostOffset = (hostOffset >> 8) + (sum >> 8)
	}

	return ip
}
