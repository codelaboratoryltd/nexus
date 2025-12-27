package state

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/keys"
	"github.com/codelaboratoryltd/nexus/internal/store"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	ds "github.com/ipfs/go-datastore"
	crdt "github.com/ipfs/go-ds-crdt"
	"github.com/ipfs/go-ds-crdt/pb"
	logging "github.com/ipfs/go-log/v2"
	libpubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	// OurName identifies Nexus nodes in the cluster.
	OurName = "Nexus"

	// DefaultEventQueueSize is the default size for the work queue.
	DefaultEventQueueSize = 1000

	// DefaultRebroadcastInterval is how often to rebroadcast state.
	DefaultRebroadcastInterval = 5 * time.Second
)

var (
	ErrPoolAlreadyExists = errors.New("pool already exists")
	ErrPoolNotFound      = errors.New("pool not found")
	ErrReadOnlyNode      = errors.New("operation not allowed on read-only node")
)

// nodeInfo represents metadata about a network node.
type nodeInfo struct {
	bestBefore time.Time
	isWrite    bool
}

// Config holds configuration for the state manager.
type Config struct {
	PrivateKey     crypto.PrivKey
	ListenPort     uint32
	Role           string // "core", "write", "read"
	Topic          string
	Bootstrap      []string
	Datastore      ds.Batching
	EventQueueSize uint32
}

// State manages pools, allocations, and nodes in a distributed system.
type State struct {
	mu              sync.Mutex
	poolStore       store.PoolStore
	allocationStore store.AllocationStore
	nodeStore       store.NodeStore
	hashRing        *hashring.VirtualHashRing
	nodes           map[string]time.Time
	ourID           string
	role            string
	topic           string
	ps              *libpubsub.PubSub
	workQueue       chan WorkItem
	syncComplete    bool
	log             *logging.ZapEventLogger
	store           *crdt.Datastore
}

// NewStateManager initializes a new State instance with P2P and CRDT.
func NewStateManager(ctx context.Context, cfg Config) (*State, error) {
	log := logging.Logger("nexus-state")

	pid, err := peer.IDFromPublicKey(cfg.PrivateKey.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("could not create libp2p peer ID: %w", err)
	}

	eventQueueSize := cfg.EventQueueSize
	if eventQueueSize == 0 {
		eventQueueSize = DefaultEventQueueSize
	}

	manager := &State{
		poolStore:       nil,
		allocationStore: nil,
		topic:           cfg.Topic,
		role:            strings.ToLower(cfg.Role),
		hashRing:        hashring.NewVirtualNodesHashRing(),
		nodes:           make(map[string]time.Time),
		workQueue:       make(chan WorkItem, eventQueueSize),
		log:             log,
	}

	listen, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.ListenPort))
	if err != nil {
		return nil, fmt.Errorf("could not create listener: %w", err)
	}

	h, dht, err := ipfslite.SetupLibp2p(
		ctx,
		cfg.PrivateKey,
		nil,
		[]multiaddr.Multiaddr{listen},
		cfg.Datastore,
		ipfslite.Libp2pOptionsExtra...,
	)
	if err != nil {
		return nil, fmt.Errorf("could not setup libp2p host: %w", err)
	}

	manager.ourID = h.ID().String()

	ps, err := libpubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("failed creation of pubsub: %w", err)
	}

	manager.ps = ps

	pubsubBC, err := crdt.NewPubSubBroadcaster(ctx, ps, cfg.Topic)
	if err != nil {
		return nil, fmt.Errorf("failed creation of broadcaster: %w", err)
	}

	ipfs, err := ipfslite.New(ctx, cfg.Datastore, nil, h, dht, &ipfslite.Config{ReprovideInterval: -1})
	if err != nil {
		return nil, fmt.Errorf("failed to create ipfs pubsub: %w", err)
	}

	opts := crdt.DefaultOptions()
	opts.Logger = log
	opts.RebroadcastInterval = DefaultRebroadcastInterval
	opts.PutHook = func(k ds.Key, v []byte) {
		manager.enqueueWork(k, v)
	}
	opts.DeleteHook = func(k ds.Key) {
		manager.enqueueWork(k, nil)
	}
	opts.NumWorkers = 50
	opts.MultiHeadProcessing = true
	opts.MembershipHook = manager.handleMembershipUpdate

	crdtDatastore, err := crdt.New(h, cfg.Datastore, ipfs.BlockStore(), keys.NexusKey, ipfs, pubsubBC, opts)
	if err != nil {
		return nil, fmt.Errorf("creating crdt: %w", err)
	}

	manager.store = crdtDatastore

	err = crdtDatastore.UpdateMeta(ctx, map[string]string{
		"name": OurName,
		"role": manager.role,
	})
	if err != nil {
		return nil, fmt.Errorf("setting metadata: %w", err)
	}

	manager.poolStore = store.NewPoolStore(crdtDatastore, keys.PoolKey)
	manager.nodeStore = store.NewNodeStore(&crdtStateStore{crdt: crdtDatastore})
	manager.allocationStore = store.NewAllocationStore(
		crdtDatastore,
		keys.AllocationKey,
		keys.SubscriberKey,
		manager.poolStore,
	)

	// Perform bootstrapping
	bootstrapPeers(ctx, log, h, cfg.Bootstrap)

	err = manager.syncState(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync state: %w", err)
	}

	manager.startWorkerPool(ctx, 1)

	log.Infof(`
	Peer ID: %s
	Role: %s
	Topic: %s
	Listen addresses:
	%s

	Ready!
	`,
		pid, cfg.Role, cfg.Topic, listenAddrs(h),
	)

	return manager, nil
}

// crdtStateStore wraps CRDT datastore to implement store.StateStore.
type crdtStateStore struct {
	crdt *crdt.Datastore
}

func (c *crdtStateStore) GetMembers(ctx context.Context) map[string]*store.NodeMember {
	state := c.crdt.GetState(ctx)
	members := make(map[string]*store.NodeMember)
	for k, v := range state.GetMembers() {
		members[k] = &store.NodeMember{
			BestBefore: v.GetBestBefore(),
			Metadata:   v.GetMetadata(),
		}
	}
	return members
}

// bootstrapPeers connects the libp2p host to the provided bootstrap peers.
func bootstrapPeers(ctx context.Context, log *logging.ZapEventLogger, h host.Host, bootstraps []string) {
	for _, addr := range bootstraps {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			log.Errorf("Invalid bootstrap address: %s", addr)
			continue
		}

		peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			log.Errorf("Could not parse bootstrap peer address: %s", addr)
			continue
		}

		log.Infof("Connecting to bootstrap peer: %s", addr)
		if err := h.Connect(ctx, *peerInfo); err != nil {
			log.Warnf("Failed to connect to bootstrap peer %s: %v", addr, err)
		}
	}
}

func listenAddrs(h host.Host) string {
	var addrs []string
	for _, c := range h.Addrs() {
		ma, _ := multiaddr.NewMultiaddr(c.String() + "/p2p/" + h.ID().String())
		addrs = append(addrs, ma.String())
	}
	return strings.Join(addrs, "\n")
}

// syncState synchronizes local state from the CRDT.
func (s *State) syncState(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Sync pools
	pools, err := s.poolStore.ListPools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list pools: %w", err)
	}
	for _, p := range pools {
		err = s.hashRing.RegisterPool(hashring.IPPool{
			ID:          hashring.PoolID(p.ID),
			Network:     &p.CIDR,
			VNodesCount: p.ShardingFactor,
		})
		if err != nil {
			return fmt.Errorf("failed syncing state: %w", err)
		}
	}

	// Sync Nodes - only write nodes participate in the hash ring
	nodes, err := s.nodeStore.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch nodes from store: %w", err)
	}

	for _, node := range nodes {
		if meta, ok := node.Metadata["name"]; !ok || meta != OurName {
			continue // Skip non-Nexus nodes
		}
		if time.Now().Before(node.BestBefore) {
			s.log.Infof("Syncing active node %s with BestBefore %v", node.ID, node.BestBefore)
			s.nodes[node.ID] = node.BestBefore

			// Only add write nodes to the hash ring
			if node.Metadata["role"] != "read" {
				if err := s.hashRing.AddNode(hashring.NodeID(node.ID)); err != nil {
					s.log.Errorf("Failed to add node %s to hash ring during sync: %v", node.ID, err)
				}
			}
		} else {
			s.log.Infof("Node %s has expired (BestBefore %v) and will not be synced", node.ID, node.BestBefore)
		}
	}

	s.log.Info("Sync process completed successfully")
	s.syncComplete = true

	return nil
}

// ID returns this node's ID.
func (s *State) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ourID
}

// Role returns this node's role.
func (s *State) Role() string {
	return s.role
}

// HashRing returns the virtual hash ring.
func (s *State) HashRing() *hashring.VirtualHashRing {
	return s.hashRing
}

// PoolStore returns the pool store.
func (s *State) PoolStore() store.PoolStore {
	return s.poolStore
}

// NodeStore returns the node store.
func (s *State) NodeStore() store.NodeStore {
	return s.nodeStore
}

// AllocationStore returns the allocation store.
func (s *State) AllocationStore() store.AllocationStore {
	return s.allocationStore
}

// GetPool retrieves a pool by ID.
func (s *State) GetPool(ctx context.Context, id string) (*store.Pool, error) {
	pool, err := s.poolStore.GetPool(ctx, id)
	if err != nil {
		return pool, fmt.Errorf("failed getting pool: %w", err)
	}
	return pool, nil
}

// CreatePool adds a new pool and broadcasts via CRDT.
func (s *State) CreatePool(ctx context.Context, pool *store.Pool) error {
	if s.role == "read" {
		return ErrReadOnlyNode
	}

	// Update via CreatePool is not allowed.
	if _, err := s.poolStore.GetPool(ctx, pool.ID); err == nil {
		return fmt.Errorf("failed to create pool %s: %w", pool.ID, ErrPoolAlreadyExists)
	}

	err := s.hashRing.RegisterPool(hashring.IPPool{
		ID:          hashring.PoolID(pool.ID),
		Network:     &pool.CIDR,
		VNodesCount: pool.ShardingFactor,
	})
	if err != nil {
		return fmt.Errorf("failed to register pool %s in hash ring: %w", pool.ID, err)
	}

	// Add the pool to the CRDT.
	if err := s.poolStore.SavePool(ctx, pool); err != nil {
		return fmt.Errorf("failed to add pool %s to CRDT: %w", pool.ID, err)
	}

	s.log.Infof("Successfully created pool %s", pool.ID)
	return nil
}

// DeletePool removes a pool.
func (s *State) DeletePool(ctx context.Context, id string) error {
	if s.role == "read" {
		return ErrReadOnlyNode
	}

	if err := s.poolStore.DeletePool(ctx, id); err != nil {
		return fmt.Errorf("failed to delete pool from CRDT: %w", err)
	}
	return nil
}

// ListPools returns all pools.
func (s *State) ListPools(ctx context.Context) ([]*store.Pool, error) {
	pools, err := s.poolStore.ListPools(ctx)
	if err != nil {
		return pools, fmt.Errorf("failed to list pools: %w", err)
	}
	return pools, nil
}

// GetAllocation retrieves a subscriber's allocation.
func (s *State) GetAllocation(ctx context.Context, subscriberID string) (*store.Allocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	allocation, err := s.allocationStore.GetAllocationBySubscriber(ctx, subscriberID)
	if err != nil {
		return allocation, fmt.Errorf("failed getting allocation: %w", err)
	}
	return allocation, nil
}

// AllocateIP allocates an IP for a subscriber within a pool.
func (s *State) AllocateIP(ctx context.Context, poolID, subscriberID string) (net.IP, error) {
	if s.role == "read" {
		return nil, ErrReadOnlyNode
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if the subscriber already has an allocation
	allocation, err := s.allocationStore.GetAllocationBySubscriber(ctx, subscriberID)
	if err != nil && !errors.Is(err, store.ErrNoAllocationFound) {
		return nil, fmt.Errorf("failed getting allocation by subscriber: %w", err)
	}
	if err == nil {
		// If the subscriber is already allocated in the requested pool, return the existing IP
		if allocation.PoolID == poolID {
			return allocation.IP, nil
		}

		// Otherwise, deallocate the old IP
		err := s.allocationStore.RemoveAllocation(ctx, allocation.PoolID, allocation.SubscriberID)
		if err != nil {
			return nil, fmt.Errorf("failed to remove allocation: %w", err)
		}
	}

	// Allocate from hashring based on subscriber ID
	ip := s.hashRing.AllocateIP(poolID, subscriberID)
	if ip == nil {
		return nil, fmt.Errorf("no available IPs in pool %s", poolID)
	}

	alloc := store.Allocation{
		PoolID:       poolID,
		IP:           ip,
		Timestamp:    time.Now(),
		SubscriberID: subscriberID,
	}

	// Persist the allocation in the datastore
	if err := s.allocationStore.SaveAllocation(ctx, &alloc); err != nil {
		return nil, fmt.Errorf("failed to persist allocation: %w", err)
	}

	return ip, nil
}

// DeallocateIP removes an IP allocation for a subscriber.
func (s *State) DeallocateIP(ctx context.Context, poolID, subscriberID string) error {
	if s.role == "read" {
		return ErrReadOnlyNode
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.allocationStore.RemoveAllocation(ctx, poolID, subscriberID)
	if err != nil {
		return fmt.Errorf("failed to remove allocation: %w", err)
	}

	return nil
}

// handleMembershipUpdate is invoked when the CRDT has new membership information.
func (s *State) handleMembershipUpdate(members map[string]*pb.Participant) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nexusNodes := make(map[string]nodeInfo)
	for id, participant := range members {
		// Filter nexus members for hashring
		if meta, ok := participant.GetMetadata()["name"]; ok && meta == OurName {
			nexusNodes[id] = nodeInfo{
				bestBefore: time.Unix(int64(participant.GetBestBefore()), 0),
				isWrite:    participant.GetMetadata()["role"] != "read",
			}
		}
	}

	// Add new nodes or update existing ones.
	for id, info := range nexusNodes {
		if _, exists := s.nodes[id]; !exists {
			s.addNewNode(id, info)
		}
		s.nodes[id] = info.bestBefore
	}

	// Remove nodes that are no longer present.
	for id := range s.nodes {
		if _, exists := nexusNodes[id]; !exists {
			if err := s.hashRing.RemoveNode(hashring.NodeID(id)); err != nil {
				s.log.Errorf("Failed to remove node %s from hash ring: %v", id, err)
			} else {
				s.log.Infof("Removed node %s from hash ring", id)
			}
			delete(s.nodes, id)
		}
	}
}

// addNewNode adds a new node to the hash ring if it's a write node.
func (s *State) addNewNode(id string, info nodeInfo) {
	if info.isWrite {
		if err := s.hashRing.AddNode(hashring.NodeID(id)); err != nil {
			s.log.Errorf("Failed to add write node %s to hash ring: %v", id, err)
		} else {
			s.log.Infof("Added write node %s to hash ring", id)
		}
	} else {
		s.log.Infof("Added read node %s (not in hash ring)", id)
	}
}

// handlePoolUpdate processes pool updates from the CRDT.
func (s *State) handlePoolUpdate(ctx context.Context, key ds.Key, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pool, err := s.poolStore.UnmarshalKey(key, value)
	if err != nil {
		return fmt.Errorf("failed to unmarshal pool key: %w", err)
	}

	hashringPool := hashring.IPPool{
		ID:          hashring.PoolID(pool.ID),
		Network:     &pool.CIDR,
		VNodesCount: pool.ShardingFactor,
	}
	err = s.hashRing.RegisterPool(hashringPool)
	if err != nil {
		return fmt.Errorf("failed to add pool to hash ring: %w", err)
	}

	return nil
}

// handlePoolDelete processes pool deletions from the CRDT.
func (s *State) handlePoolDelete(ctx context.Context, key ds.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pool, err := s.poolStore.UnmarshalKey(key, nil)
	if err != nil {
		return fmt.Errorf("failed to unmarshal pool key: %w", err)
	}

	err = s.hashRing.UnregisterPool(hashring.PoolID(pool.ID))
	if err != nil {
		return fmt.Errorf("failed to remove pool from hash ring: %w", err)
	}

	return nil
}

// handleAllocation processes allocation updates from the CRDT.
func (s *State) handleAllocation(ctx context.Context, key ds.Key, value []byte) error {
	if s.role == "read" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ar, err := s.allocationStore.UnmarshalAllocationKey(key)
	if err != nil {
		return fmt.Errorf("failed to unmarshal allocation key: %w", err)
	}

	s.log.Infof("Processed allocation for subscriber %s in pool %s", ar.SubscriberID, ar.PoolID)
	return nil
}

// handleDeallocation processes deallocation from the CRDT.
func (s *State) handleDeallocation(ctx context.Context, key ds.Key) error {
	if s.role == "read" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ar, err := s.allocationStore.UnmarshalAllocationKey(key)
	if err != nil {
		return fmt.Errorf("failed to unmarshal allocation key: %w", err)
	}

	s.log.Infof("Processed deallocation for subscriber %s in pool %s", ar.SubscriberID, ar.PoolID)
	return nil
}

// GracefulShutdownOptions configures graceful shutdown behavior.
type GracefulShutdownOptions struct {
	MaxWaitTime     time.Duration
	GracePeriod     time.Duration
	CheckInterval   time.Duration
	PeerSelector    func(metadata map[string]string) bool
	RequiredMatches int
}

// GracefulShutdown performs a graceful shutdown of the CRDT datastore.
func (s *State) GracefulShutdown(ctx context.Context, opts *GracefulShutdownOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	crdtOpts := &crdt.GracefulShutdownOptions{
		MaxWaitTime:     opts.MaxWaitTime,
		GracePeriod:     opts.GracePeriod,
		CheckInterval:   opts.CheckInterval,
		RequiredMatches: opts.RequiredMatches,
	}

	if opts.PeerSelector != nil {
		crdtOpts.PeerSelector = func(metadata map[string]string) bool {
			return opts.PeerSelector(metadata)
		}
	}

	if err := s.store.GracefulShutdown(ctx, crdtOpts); err != nil {
		return fmt.Errorf("failed to perform graceful shutdown: %w", err)
	}
	return nil
}
