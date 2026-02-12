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
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	mss "github.com/multiformats/go-multistream"
	rendezvous "github.com/waku-org/go-libp2p-rendezvous"
)

const (
	// OurName identifies Nexus nodes in the cluster.
	OurName = "Nexus"

	// DefaultEventQueueSize is the default size for the work queue.
	DefaultEventQueueSize = 1000

	// DefaultRebroadcastInterval is how often to rebroadcast state.
	DefaultRebroadcastInterval = 5 * time.Second

	// DefaultSyncLagThreshold is the default maximum acceptable sync lag
	// before considering the node not ready. If the time since last successful
	// sync with any peer exceeds this, the node reports as not ready.
	DefaultSyncLagThreshold = 30 * time.Second
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

// DiscoveryMode defines the peer discovery method.
type DiscoveryMode string

const (
	// DiscoveryModeNone disables automatic peer discovery.
	DiscoveryModeNone DiscoveryMode = "none"
	// DiscoveryModeRendezvous uses a rendezvous server for peer discovery.
	DiscoveryModeRendezvous DiscoveryMode = "rendezvous"
	// DiscoveryModeDNS uses Kubernetes headless service DNS resolution.
	DiscoveryModeDNS DiscoveryMode = "dns"
)

// Config holds configuration for the state manager.
type Config struct {
	PrivateKey     crypto.PrivKey
	ListenPort     uint32
	Role           string // "core", "write", "read"
	Topic          string
	Bootstrap      []string
	Datastore      ds.Batching
	EventQueueSize uint32

	// Discovery mode configuration
	DiscoveryMode DiscoveryMode // "none", "rendezvous", "dns"

	// Rendezvous discovery configuration
	RendezvousServer    string // Multiaddr of rendezvous server (e.g., "/ip4/127.0.0.1/tcp/8765/p2p/QmPeerID")
	RendezvousNamespace string // Namespace for peer discovery (default: "nexus")

	// DNS discovery configuration (for Kubernetes headless services)
	DNSServiceName  string        // Headless service DNS name (e.g., "nexus.namespace.svc.cluster.local")
	DNSPollInterval time.Duration // How often to poll DNS (default: 10s)
	DNSReadyTimeout time.Duration // Timeout for first peer discovery before marking ready anyway (default: 30s)

	// CRDT sync health configuration
	SyncLagThreshold time.Duration // Max acceptable sync lag before marking not ready (default: 30s)
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
	host            host.Host      // libp2p host for peer connections
	privateKey      crypto.PrivKey // libp2p private key for probe connections

	// Peer discovery status for readiness probe
	peerDiscoveryMu    sync.RWMutex
	peerDiscovered     bool      // At least one peer has been discovered
	peerDiscoveryReady bool      // Ready for traffic (peer found or timeout reached)
	peerDiscoveryStart time.Time // When discovery started
	dnsReadyTimeout    time.Duration

	// CRDT sync tracking for health endpoint
	syncMu           sync.RWMutex
	peerLastSync     map[string]time.Time // Last successful sync time per peer
	syncLagThreshold time.Duration        // Max acceptable lag before not-ready

	// Config watch streaming
	watchHub *WatchHub
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

	dnsReadyTimeout := cfg.DNSReadyTimeout
	if dnsReadyTimeout == 0 {
		dnsReadyTimeout = 30 * time.Second
	}

	syncLagThreshold := cfg.SyncLagThreshold
	if syncLagThreshold == 0 {
		syncLagThreshold = DefaultSyncLagThreshold
	}

	manager := &State{
		poolStore:          nil,
		allocationStore:    nil,
		topic:              cfg.Topic,
		role:               strings.ToLower(cfg.Role),
		hashRing:           hashring.NewVirtualNodesHashRing(),
		nodes:              make(map[string]time.Time),
		workQueue:          make(chan WorkItem, eventQueueSize),
		log:                log,
		peerDiscoveryStart: time.Now(),
		dnsReadyTimeout:    dnsReadyTimeout,
		peerLastSync:       make(map[string]time.Time),
		syncLagThreshold:   syncLagThreshold,
		watchHub:           NewWatchHub(1000),
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
	manager.host = h
	manager.privateKey = cfg.PrivateKey

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
		manager.watchHub.Publish("put", k.String(), v)
	}
	opts.DeleteHook = func(k ds.Key) {
		manager.enqueueWork(k, nil)
		manager.watchHub.Publish("delete", k.String(), nil)
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

	// Start peer discovery based on configured mode
	switch cfg.DiscoveryMode {
	case DiscoveryModeRendezvous:
		// Rendezvous discovery mode
		if cfg.RendezvousServer != "" {
			namespace := cfg.RendezvousNamespace
			if namespace == "" {
				namespace = "nexus"
			}
			go manager.runRendezvousDiscovery(ctx, h, cfg.RendezvousServer, namespace)
		} else {
			log.Warn("Rendezvous discovery mode selected but no server configured")
			manager.markPeerDiscoveryReady()
		}
	case DiscoveryModeDNS:
		// DNS discovery mode for Kubernetes headless services
		if cfg.DNSServiceName != "" {
			pollInterval := cfg.DNSPollInterval
			if pollInterval == 0 {
				pollInterval = 10 * time.Second
			}
			go manager.runDNSDiscovery(ctx, h, cfg.DNSServiceName, pollInterval)
		} else {
			log.Warn("DNS discovery mode selected but no service name configured")
			manager.markPeerDiscoveryReady()
		}
	default:
		// No discovery mode - mark as ready immediately
		manager.markPeerDiscoveryReady()
	}

	// Legacy support: start rendezvous discovery if server is configured but mode is not set
	if cfg.DiscoveryMode == "" && cfg.RendezvousServer != "" {
		namespace := cfg.RendezvousNamespace
		if namespace == "" {
			namespace = "nexus"
		}
		go manager.runRendezvousDiscovery(ctx, h, cfg.RendezvousServer, namespace)
	}

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

// runRendezvousDiscovery registers with a rendezvous server and discovers peers.
// This enables peer discovery in environments where mDNS doesn't work (e.g., Kubernetes).
func (s *State) runRendezvousDiscovery(ctx context.Context, h host.Host, serverAddr, namespace string) {
	// Parse the rendezvous server multiaddr
	ma, err := multiaddr.NewMultiaddr(serverAddr)
	if err != nil {
		s.log.Errorf("Invalid rendezvous server address %s: %v", serverAddr, err)
		return
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		s.log.Errorf("Could not parse rendezvous server peer address %s: %v", serverAddr, err)
		return
	}

	s.log.Infof("Connecting to rendezvous server: %s", serverAddr)

	// Connect to the rendezvous server
	if err := h.Connect(ctx, *peerInfo); err != nil {
		s.log.Errorf("Failed to connect to rendezvous server %s: %v", serverAddr, err)
		return
	}

	s.log.Infof("Connected to rendezvous server, starting discovery for namespace: %s", namespace)

	// Create rendezvous discovery using the server peer ID
	discovery := rendezvous.NewRendezvousDiscovery(h, peerInfo.ID)

	// Advertise our presence in the namespace
	// TTL of 2 hours (7200 seconds) is the default
	util.Advertise(ctx, discovery, namespace)
	s.log.Infof("Registered with rendezvous server in namespace: %s", namespace)

	// Continuously discover and connect to peers
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Discover immediately, then periodically
	s.discoverPeers(ctx, h, discovery, namespace)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("Rendezvous discovery stopped")
			return
		case <-ticker.C:
			s.discoverPeers(ctx, h, discovery, namespace)
		}
	}
}

// discoverPeers finds and connects to peers in the rendezvous namespace.
func (s *State) discoverPeers(ctx context.Context, h host.Host, disc discovery.Discovery, namespace string) {
	peerChan, err := disc.FindPeers(ctx, namespace)
	if err != nil {
		s.log.Warnf("Failed to find peers in namespace %s: %v", namespace, err)
		return
	}

	for p := range peerChan {
		// Skip ourselves
		if p.ID == h.ID() {
			continue
		}

		// Skip if already connected
		if h.Network().Connectedness(p.ID) == 1 { // 1 = Connected
			s.markPeerDiscovered()
			continue
		}

		s.log.Infof("Discovered peer via rendezvous: %s", p.ID)

		// Attempt to connect
		if err := h.Connect(ctx, p); err != nil {
			s.log.Warnf("Failed to connect to discovered peer %s: %v", p.ID, err)
		} else {
			s.log.Infof("Connected to peer via rendezvous: %s", p.ID)
			s.markPeerDiscovered()
		}
	}
}

// runDNSDiscovery periodically resolves a headless service DNS name and connects to discovered peers.
// This is designed for Kubernetes environments where a headless service returns all pod IPs.
func (s *State) runDNSDiscovery(ctx context.Context, h host.Host, serviceName string, pollInterval time.Duration) {
	s.log.Infof("Starting DNS discovery for service: %s (poll interval: %s)", serviceName, pollInterval)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Start a goroutine to check for ready timeout
	go s.watchReadyTimeout(ctx)

	// Discover immediately, then periodically
	s.discoverPeersViaDNS(ctx, h, serviceName)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("DNS discovery stopped")
			return
		case <-ticker.C:
			s.discoverPeersViaDNS(ctx, h, serviceName)
		}
	}
}

// discoverPeersViaDNS resolves the DNS name and connects to each discovered peer IP.
func (s *State) discoverPeersViaDNS(ctx context.Context, h host.Host, serviceName string) {
	// Resolve the headless service DNS name to get all pod IPs
	ips, err := net.LookupIP(serviceName)
	if err != nil {
		s.log.Warnf("Failed to resolve DNS for %s: %v", serviceName, err)
		return
	}

	if len(ips) == 0 {
		s.log.Debugf("No IPs found for DNS name %s", serviceName)
		return
	}

	s.log.Debugf("DNS discovery found %d IPs for %s", len(ips), serviceName)

	// Get our own IPs to skip ourselves
	ourAddrs := h.Addrs()
	ourIPs := make(map[string]bool)
	for _, addr := range ourAddrs {
		// Extract IP from multiaddr
		if ip, err := addr.ValueForProtocol(multiaddr.P_IP4); err == nil {
			ourIPs[ip] = true
		}
		if ip, err := addr.ValueForProtocol(multiaddr.P_IP6); err == nil {
			ourIPs[ip] = true
		}
	}

	// Get the P2P port from our listen addresses
	var p2pPort string
	for _, addr := range ourAddrs {
		if port, err := addr.ValueForProtocol(multiaddr.P_TCP); err == nil {
			p2pPort = port
			break
		}
	}
	if p2pPort == "" {
		s.log.Warn("Could not determine P2P port from listen addresses")
		return
	}

	for _, ip := range ips {
		ipStr := ip.String()

		// Skip our own IP
		if ourIPs[ipStr] {
			continue
		}

		// Build multiaddr for this peer
		var maStr string
		if ip.To4() != nil {
			maStr = fmt.Sprintf("/ip4/%s/tcp/%s", ipStr, p2pPort)
		} else {
			maStr = fmt.Sprintf("/ip6/%s/tcp/%s", ipStr, p2pPort)
		}

		ma, err := multiaddr.NewMultiaddr(maStr)
		if err != nil {
			s.log.Warnf("Failed to create multiaddr for %s: %v", ipStr, err)
			continue
		}

		// Check if we're already connected to this address
		connected := false
		for _, conn := range h.Network().Conns() {
			for _, addr := range conn.RemoteMultiaddr().Protocols() {
				if addr.Code == multiaddr.P_IP4 || addr.Code == multiaddr.P_IP6 {
					remoteIP, _ := conn.RemoteMultiaddr().ValueForProtocol(addr.Code)
					if remoteIP == ipStr {
						connected = true
						s.markPeerDiscovered()
						break
					}
				}
			}
			if connected {
				break
			}
		}

		if connected {
			continue
		}

		s.log.Infof("Discovered peer via DNS: %s", maStr)

		// For DNS discovery without known peer IDs, we need to dial directly
		// The libp2p identify protocol will exchange peer IDs upon connection
		if err := s.dialPeerByAddress(ctx, h, ma); err != nil {
			s.log.Debugf("Failed to connect to DNS-discovered peer %s: %v", maStr, err)
		} else {
			s.log.Infof("Connected to peer via DNS: %s", maStr)
			s.markPeerDiscovered()
		}
	}
}

// dialPeerByAddress attempts to connect to a peer given only their address.
// This is used for DNS discovery where we don't know the peer ID ahead of time.
// It performs a probe connection using a raw TCP + Noise handshake to discover
// the remote peer ID, then establishes a proper libp2p connection.
func (s *State) dialPeerByAddress(ctx context.Context, h host.Host, addr multiaddr.Multiaddr) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Probe: open a raw TCP connection and perform a Noise handshake to
	// discover the remote peer ID without requiring it in advance.
	peerID, err := s.probePeerID(dialCtx, addr)
	if err != nil {
		return fmt.Errorf("probe peer ID: %w", err)
	}

	// Skip if the probed peer is ourselves
	if peerID == h.ID() {
		return nil
	}

	// Now that we know the peer ID, add the address to the peerstore and connect properly.
	h.Peerstore().AddAddrs(peerID, []multiaddr.Multiaddr{addr}, peerstore.TempAddrTTL)
	return h.Connect(dialCtx, peer.AddrInfo{
		ID:    peerID,
		Addrs: []multiaddr.Multiaddr{addr},
	})
}

// probePeerID dials a raw TCP connection to addr, performs multistream-select
// and a Noise handshake with peer ID checking disabled (accepts any peer),
// and returns the discovered remote peer ID.
func (s *State) probePeerID(ctx context.Context, addr multiaddr.Multiaddr) (peer.ID, error) {
	// Dial raw TCP via multiaddr
	dialer := &manet.Dialer{}
	conn, err := dialer.DialContext(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("raw dial: %w", err)
	}
	defer conn.Close()

	// Negotiate the Noise security protocol via multistream-select
	noiseProtoID := protocol.ID(noise.ID)
	_, err = mss.SelectOneOf([]string{string(noiseProtoID)}, conn)
	if err != nil {
		return "", fmt.Errorf("multistream select: %w", err)
	}

	// Create a Noise transport with peer ID checking disabled so we can
	// discover the remote peer's identity from the handshake.
	noiseTpt, err := noise.New(noiseProtoID, s.privateKey, nil)
	if err != nil {
		return "", fmt.Errorf("create noise transport: %w", err)
	}
	sessionTpt, err := noiseTpt.WithSessionOptions(noise.DisablePeerIDCheck())
	if err != nil {
		return "", fmt.Errorf("create session transport: %w", err)
	}

	secConn, err := sessionTpt.SecureOutbound(ctx, conn, "")
	if err != nil {
		return "", fmt.Errorf("noise handshake: %w", err)
	}
	defer secConn.Close()

	return secConn.RemotePeer(), nil
}

// markPeerDiscovered marks that at least one peer has been discovered.
func (s *State) markPeerDiscovered() {
	s.peerDiscoveryMu.Lock()
	defer s.peerDiscoveryMu.Unlock()

	if !s.peerDiscovered {
		s.log.Info("First peer discovered, marking discovery ready")
		s.peerDiscovered = true
		s.peerDiscoveryReady = true
	}
}

// markPeerDiscoveryReady marks the node as ready for traffic without requiring peer discovery.
func (s *State) markPeerDiscoveryReady() {
	s.peerDiscoveryMu.Lock()
	defer s.peerDiscoveryMu.Unlock()
	s.peerDiscoveryReady = true
}

// watchReadyTimeout monitors for the ready timeout and marks the node ready if no peers found.
func (s *State) watchReadyTimeout(ctx context.Context) {
	timer := time.NewTimer(s.dnsReadyTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		s.peerDiscoveryMu.Lock()
		if !s.peerDiscoveryReady {
			s.log.Infof("Peer discovery timeout reached (%s), marking ready anyway (first node in cluster)", s.dnsReadyTimeout)
			s.peerDiscoveryReady = true
		}
		s.peerDiscoveryMu.Unlock()
	}
}

// IsPeerDiscoveryReady returns true if the node is ready for traffic.
// This is true if either a peer has been discovered or the ready timeout has been reached.
func (s *State) IsPeerDiscoveryReady() bool {
	s.peerDiscoveryMu.RLock()
	defer s.peerDiscoveryMu.RUnlock()
	return s.peerDiscoveryReady
}

// CRDTSyncStatus represents the current CRDT sync health.
type CRDTSyncStatus struct {
	PeersConnected int       // Number of peers with sync data
	SyncLagMs      int64     // Max sync lag across all peers in milliseconds
	LastSync       time.Time // Most recent sync time across all peers
}

// CRDTSyncStatus returns the current CRDT sync health status.
// The sync lag is the maximum time since last successful sync across all peers.
func (s *State) CRDTSyncStatus() CRDTSyncStatus {
	s.syncMu.RLock()
	defer s.syncMu.RUnlock()

	status := CRDTSyncStatus{
		PeersConnected: len(s.peerLastSync),
	}

	if len(s.peerLastSync) == 0 {
		return status
	}

	now := time.Now()
	var maxLag time.Duration
	var latest time.Time

	for _, lastSync := range s.peerLastSync {
		lag := now.Sub(lastSync)
		if lag > maxLag {
			maxLag = lag
		}
		if lastSync.After(latest) {
			latest = lastSync
		}
	}

	status.SyncLagMs = maxLag.Milliseconds()
	status.LastSync = latest
	return status
}

// IsCRDTSyncHealthy returns true if the CRDT sync lag is within threshold.
// If there are no peers, this returns true (standalone/first node).
func (s *State) IsCRDTSyncHealthy() bool {
	s.syncMu.RLock()
	defer s.syncMu.RUnlock()

	if len(s.peerLastSync) == 0 {
		return true
	}

	now := time.Now()
	for _, lastSync := range s.peerLastSync {
		if now.Sub(lastSync) > s.syncLagThreshold {
			return false
		}
	}
	return true
}

// PeerDiscoveryStatus returns the current peer discovery status.
func (s *State) PeerDiscoveryStatus() (discovered bool, ready bool, elapsed time.Duration) {
	s.peerDiscoveryMu.RLock()
	defer s.peerDiscoveryMu.RUnlock()
	return s.peerDiscovered, s.peerDiscoveryReady, time.Since(s.peerDiscoveryStart)
}

// ConnectedPeerCount returns the number of connected peers.
func (s *State) ConnectedPeerCount() int {
	if s.host == nil {
		return 0
	}
	return len(s.host.Network().Peers())
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

// WatchHub returns the change event hub for config streaming.
func (s *State) WatchHub() *WatchHub {
	return s.watchHub
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

	now := time.Now()

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

	// Record sync time for all peers (excluding ourselves)
	s.syncMu.Lock()
	for id := range nexusNodes {
		if id != s.ourID {
			s.peerLastSync[id] = now
		}
	}
	// Remove stale peer entries
	for id := range s.peerLastSync {
		if _, exists := nexusNodes[id]; !exists {
			delete(s.peerLastSync, id)
		}
	}
	peerCount := len(s.peerLastSync)
	var maxLag float64
	for _, lastSync := range s.peerLastSync {
		lag := now.Sub(lastSync).Seconds()
		if lag > maxLag {
			maxLag = lag
		}
	}
	s.syncMu.Unlock()

	// Update Prometheus metrics
	crdtPeersConnectedGauge.Set(float64(peerCount))
	crdtSyncLagGauge.Set(maxLag)

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
// It announces departure to peers, waits for acknowledgment, and updates
// shutdown metrics accordingly.
func (s *State) GracefulShutdown(ctx context.Context, opts *GracefulShutdownOptions) error {
	s.log.Info("Starting graceful shutdown: announcing departure to peers")

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
		ungracefulShutdownsTotal.Inc()
		return fmt.Errorf("failed to perform graceful shutdown: %w", err)
	}

	gracefulShutdownsTotal.Inc()
	s.log.Info("Graceful shutdown complete: peers acknowledged departure")
	return nil
}

// Close shuts down the libp2p host. Call after GracefulShutdown.
func (s *State) Close() error {
	if s.host != nil {
		return s.host.Close()
	}
	return nil
}
