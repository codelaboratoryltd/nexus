package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/gorilla/mux"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	badgerds "github.com/ipfs/go-ds-badger4"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	libp2pHost "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	rendezvous "github.com/waku-org/go-libp2p-rendezvous"
	dbi "github.com/waku-org/go-libp2p-rendezvous/db"

	"github.com/codelaboratoryltd/nexus/internal/api"
	"github.com/codelaboratoryltd/nexus/internal/auth"
	nexusgrpc "github.com/codelaboratoryltd/nexus/internal/grpc"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/keys"
	"github.com/codelaboratoryltd/nexus/internal/rendezvousdb"
	"github.com/codelaboratoryltd/nexus/internal/state"
	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/ztp"
)

var (
	// Build info (set at compile time)
	BuildVersion = "dev"
	BuildCommit  = "unknown"
)

// Config holds server configuration
type Config struct {
	NodeID      string
	Role        string
	HTTPPort    int
	MetricsPort int
	P2PPort     int
	DataPath    string
	Bootstrap   []string
	P2PEnabled  bool

	// Discovery mode config
	DiscoveryMode string // "none", "rendezvous", "dns"

	// Rendezvous discovery config
	RendezvousServer    string
	RendezvousNamespace string

	// DNS discovery config (for Kubernetes headless services)
	DNSServiceName  string
	DNSPollInterval time.Duration
	DNSReadyTimeout time.Duration

	// ZTP DHCP server config
	ZTPEnabled   bool
	ZTPInterface string
	ZTPNetwork   string
	ZTPGateway   string
	ZTPDNS       string

	// Rate limiting config
	RateLimit      int // Max requests per minute per client
	RateLimitBurst int // Burst size (max tokens)

	// gRPC config
	GRPCPort    int
	GRPCTLSCert string
	GRPCTLSKey  string
}

func main() {
	if err := rootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func rootCommand() *cobra.Command {
	var cfg Config

	root := &cobra.Command{
		Use:   "nexus",
		Short: "Nexus - Central coordination service for BNG edge networks",
		Long: `Nexus provides central coordination for distributed OLT-BNG deployments:
  - Resource management (IPs, VLANs, ports)
  - OLT bootstrap and registration
  - Configuration distribution
  - Distributed state via CLSet CRDT`,
	}

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the Nexus server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Allow environment variables to override flags
			if v := os.Getenv("NEXUS_DISCOVERY"); v != "" && cfg.DiscoveryMode == "" {
				cfg.DiscoveryMode = v
			}
			if v := os.Getenv("NEXUS_RENDEZVOUS_SERVER"); v != "" && cfg.RendezvousServer == "" {
				cfg.RendezvousServer = v
			}
			if v := os.Getenv("NEXUS_RENDEZVOUS_NAMESPACE"); v != "" && cfg.RendezvousNamespace == "nexus" {
				cfg.RendezvousNamespace = v
			}
			if v := os.Getenv("NEXUS_DNS_SERVICE_NAME"); v != "" && cfg.DNSServiceName == "" {
				cfg.DNSServiceName = v
			}
			return runServer(cfg)
		},
	}

	serve.Flags().StringVar(&cfg.NodeID, "node-id", "", "Node identifier (auto-generated if empty)")
	serve.Flags().StringVar(&cfg.Role, "role", "core", "Node role: core, write, read")
	serve.Flags().IntVar(&cfg.HTTPPort, "http-port", 9000, "HTTP API port")
	serve.Flags().IntVar(&cfg.MetricsPort, "metrics-port", 9002, "Prometheus metrics port")
	serve.Flags().IntVar(&cfg.P2PPort, "p2p-port", 33123, "P2P listen port")
	serve.Flags().StringVar(&cfg.DataPath, "data-path", "data", "Data directory path")
	serve.Flags().StringSliceVar(&cfg.Bootstrap, "bootstrap", nil, "Bootstrap peer addresses")
	serve.Flags().BoolVar(&cfg.P2PEnabled, "p2p", false, "Enable P2P mode with CLSet CRDT")
	// Discovery mode flags
	serve.Flags().StringVar(&cfg.DiscoveryMode, "discovery", "", "Peer discovery mode: none, rendezvous, dns (default: auto-detect)")

	// Rendezvous discovery flags
	serve.Flags().StringVar(&cfg.RendezvousServer, "rendezvous-server", "", "Rendezvous server multiaddr for P2P discovery (e.g., /ip4/x.x.x.x/tcp/8765/p2p/QmPeerID)")
	serve.Flags().StringVar(&cfg.RendezvousNamespace, "rendezvous-namespace", "nexus", "Rendezvous namespace for peer discovery")

	// DNS discovery flags (for Kubernetes headless services)
	serve.Flags().StringVar(&cfg.DNSServiceName, "dns-service-name", "", "Headless service DNS name for peer discovery (e.g., nexus.namespace.svc.cluster.local)")
	serve.Flags().DurationVar(&cfg.DNSPollInterval, "dns-poll-interval", 10*time.Second, "How often to poll DNS for peer discovery")
	serve.Flags().DurationVar(&cfg.DNSReadyTimeout, "dns-ready-timeout", 30*time.Second, "Timeout before marking ready without peers (for first node)")

	// Rate limiting flags
	serve.Flags().IntVar(&cfg.RateLimit, "rate-limit", 100, "Maximum requests per minute per client (0 to disable)")
	serve.Flags().IntVar(&cfg.RateLimitBurst, "rate-limit-burst", 200, "Rate limit burst size (max tokens per client)")

	// ZTP DHCP server flags
	serve.Flags().BoolVar(&cfg.ZTPEnabled, "ztp", false, "Enable ZTP DHCP server for OLT-BNG provisioning")
	serve.Flags().StringVar(&cfg.ZTPInterface, "ztp-interface", "eth0", "Interface for ZTP DHCP server")
	serve.Flags().StringVar(&cfg.ZTPNetwork, "ztp-network", "192.168.100.0/24", "Management network CIDR for OLT-BNG devices")
	serve.Flags().StringVar(&cfg.ZTPGateway, "ztp-gateway", "", "Gateway IP for management network (defaults to first IP)")
	serve.Flags().StringVar(&cfg.ZTPDNS, "ztp-dns", "8.8.8.8,8.8.4.4", "DNS servers (comma-separated)")

	// gRPC server flags
	serve.Flags().IntVar(&cfg.GRPCPort, "grpc-port", 9090, "gRPC API port (0 to disable)")
	serve.Flags().StringVar(&cfg.GRPCTLSCert, "grpc-tls-cert", "", "TLS certificate file for gRPC server")
	serve.Flags().StringVar(&cfg.GRPCTLSKey, "grpc-tls-key", "", "TLS key file for gRPC server")

	version := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Nexus %s (%s)\n", BuildVersion, BuildCommit)
		},
	}

	// Rendezvous server command
	var rvzPort int
	var rvzDataPath string
	var rvzDBBackend string
	rendezvousCmd := &cobra.Command{
		Use:   "rendezvous",
		Short: "Run a libp2p rendezvous server for peer discovery",
		Long: `Run a rendezvous server that enables P2P peer discovery in environments
where mDNS doesn't work (e.g., Kubernetes). Nexus nodes can connect to this
server to register themselves and discover other peers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRendezvousServer(rvzPort, rvzDataPath, rvzDBBackend)
		},
	}
	rendezvousCmd.Flags().IntVar(&rvzPort, "port", 8765, "Rendezvous server listen port")
	rendezvousCmd.Flags().StringVar(&rvzDataPath, "data-path", "rendezvous-data", "Data directory for rendezvous server")
	rendezvousCmd.Flags().StringVar(&rvzDBBackend, "db-backend", "memory", "Database backend: memory (default) or badger")

	// Peer ID command - prints the peer ID for a given data path
	var peerIDDataPath string
	peerIDCmd := &cobra.Command{
		Use:   "peer-id",
		Short: "Print the peer ID for a given data directory",
		Long: `Print the libp2p peer ID derived from the private key in the data directory.
Useful for configuring rendezvous server addresses for clients.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pk, err := loadOrGenerateKey(peerIDDataPath)
			if err != nil {
				return fmt.Errorf("failed to load key: %w", err)
			}
			pid, err := peer.IDFromPublicKey(pk.GetPublic())
			if err != nil {
				return fmt.Errorf("failed to get peer ID: %w", err)
			}
			fmt.Println(pid.String())
			return nil
		},
	}
	peerIDCmd.Flags().StringVar(&peerIDDataPath, "data-path", "data", "Data directory containing node.key")

	root.AddCommand(serve, version, rendezvousCmd, peerIDCmd)
	return root
}

func runServer(cfg Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Printf("Starting Nexus %s (%s)\n", BuildVersion, BuildCommit)
	fmt.Printf("  Role:     %s\n", cfg.Role)
	fmt.Printf("  HTTP:     http://localhost:%d\n", cfg.HTTPPort)
	fmt.Printf("  Metrics:  http://localhost:%d/metrics\n", cfg.MetricsPort)
	fmt.Printf("  P2P:      %v (port %d)\n", cfg.P2PEnabled, cfg.P2PPort)
	if cfg.DiscoveryMode != "" {
		fmt.Printf("  Discovery: %s\n", cfg.DiscoveryMode)
	}
	if cfg.RendezvousServer != "" {
		fmt.Printf("  Rendezvous: %s (namespace: %s)\n", cfg.RendezvousServer, cfg.RendezvousNamespace)
	}
	if cfg.DNSServiceName != "" {
		fmt.Printf("  DNS Service: %s (poll: %s, timeout: %s)\n", cfg.DNSServiceName, cfg.DNSPollInterval, cfg.DNSReadyTimeout)
	}
	if cfg.GRPCPort > 0 {
		fmt.Printf("  gRPC:     localhost:%d\n", cfg.GRPCPort)
	}
	fmt.Printf("  ZTP:      %v\n", cfg.ZTPEnabled)

	var (
		ring         *hashring.VirtualHashRing
		poolStore    store.PoolStore
		nodeStore    store.NodeStore
		allocStore   store.AllocationStore
		stateManager *state.State
	)

	if cfg.P2PEnabled {
		// P2P mode with CLSet CRDT
		fmt.Println("  Mode:     P2P (CLSet CRDT)")

		// Create data directory
		if err := os.MkdirAll(cfg.DataPath, 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}

		// Initialize badger datastore
		opts := badger.DefaultOptions(cfg.DataPath)
		opts.Logger = nil // Disable badger logging
		bds, err := badgerds.NewDatastore(cfg.DataPath, &badgerds.DefaultOptions)
		if err != nil {
			return fmt.Errorf("failed to create badger datastore: %w", err)
		}
		defer bds.Close()

		// Generate or load private key
		pk, err := loadOrGenerateKey(cfg.DataPath)
		if err != nil {
			return fmt.Errorf("failed to load/generate key: %w", err)
		}

		// Initialize state manager with P2P
		stateCfg := state.Config{
			PrivateKey:          pk,
			ListenPort:          uint32(cfg.P2PPort),
			Role:                cfg.Role,
			Topic:               "nexus-state",
			Bootstrap:           cfg.Bootstrap,
			Datastore:           bds,
			EventQueueSize:      1000,
			DiscoveryMode:       state.DiscoveryMode(cfg.DiscoveryMode),
			RendezvousServer:    cfg.RendezvousServer,
			RendezvousNamespace: cfg.RendezvousNamespace,
			DNSServiceName:      cfg.DNSServiceName,
			DNSPollInterval:     cfg.DNSPollInterval,
			DNSReadyTimeout:     cfg.DNSReadyTimeout,
		}

		stateManager, err = state.NewStateManager(ctx, stateCfg)
		if err != nil {
			return fmt.Errorf("failed to create state manager: %w", err)
		}

		ring = stateManager.HashRing()
		poolStore = stateManager.PoolStore()
		nodeStore = stateManager.NodeStore()
		allocStore = stateManager.AllocationStore()

		fmt.Printf("  Peer ID:  %s\n", stateManager.ID())
	} else {
		// Standalone mode with in-memory datastore
		fmt.Println("  Mode:     Standalone (in-memory)")

		ring = hashring.NewVirtualNodesHashRing()
		memDS := dssync.MutexWrap(ds.NewMapDatastore())

		poolStore = store.NewPoolStore(memDS, keys.PoolKey)
		nodeStore = store.NewNodeStore(&memoryStateStore{})
		allocStore = store.NewAllocationStore(
			memDS,
			keys.AllocationKey,
			keys.SubscriberKey,
			poolStore,
		)
	}

	// Create API server
	apiServer := api.NewServer(ring, poolStore, nodeStore, allocStore)

	// Set readiness checker if in P2P mode with DNS discovery
	if stateManager != nil && (cfg.DiscoveryMode == "dns" || cfg.DNSServiceName != "") {
		apiServer.SetReadinessChecker(stateManager)
	}

	// Enable config watch streaming in P2P mode
	if stateManager != nil {
		apiServer.SetConfigWatcher(stateManager)
	}

	// Create HTTP router
	router := mux.NewRouter()

	// Add rate limiting middleware if enabled
	if cfg.RateLimit > 0 {
		rateLimiter := auth.NewRateLimiter(&auth.RateLimitConfig{
			MaxRequests:  cfg.RateLimit,
			Window:       time.Minute,
			ByPrincipal:  false,
			CleanupEvery: 5 * time.Minute,
		})
		defer rateLimiter.Stop()
		router.Use(rateLimiter.Middleware())
		fmt.Printf("  Rate limit: %d req/min (burst: %d)\n", cfg.RateLimit, cfg.RateLimitBurst)
	}

	apiServer.RegisterRoutes(router)

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: router,
	}

	// Start metrics server
	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: metricsRouter,
	}

	// Start servers in goroutines
	errCh := make(chan error, 3)

	go func() {
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	go func() {
		if err := metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics server error: %w", err)
		}
	}()

	// Start gRPC server if enabled
	var grpcServer *nexusgrpc.Server
	if cfg.GRPCPort > 0 {
		grpcCfg := nexusgrpc.ServerConfig{
			Port:    cfg.GRPCPort,
			TLSCert: cfg.GRPCTLSCert,
			TLSKey:  cfg.GRPCTLSKey,
		}
		var err error
		grpcServer, err = nexusgrpc.NewServer(grpcCfg, ring, poolStore, nodeStore, allocStore)
		if err != nil {
			return fmt.Errorf("failed to create gRPC server: %w", err)
		}

		go func() {
			if err := grpcServer.Serve(); err != nil {
				errCh <- fmt.Errorf("gRPC server error: %w", err)
			}
		}()
	}

	// Start ZTP DHCP server if enabled
	var ztpServer *ztp.Server
	if cfg.ZTPEnabled {
		ztpCfg, err := parseZTPConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to parse ZTP config: %w", err)
		}

		ztpServer, err = ztp.NewServer(ztpCfg)
		if err != nil {
			return fmt.Errorf("failed to create ZTP server: %w", err)
		}

		// Set up callbacks for device discovery/leasing
		ztpServer.OnDeviceDiscovered = func(mac net.HardwareAddr, hostname, vendorInfo string) {
			fmt.Printf("ZTP: Device discovered - MAC=%s hostname=%s vendor=%s\n", mac, hostname, vendorInfo)
		}
		ztpServer.OnDeviceLeased = func(mac net.HardwareAddr, ip net.IP) {
			fmt.Printf("ZTP: Lease granted - MAC=%s IP=%s\n", mac, ip)
			// TODO: Register device with node store
		}

		go func() {
			fmt.Printf("  ZTP DHCP: listening on %s (%s)\n", cfg.ZTPInterface, cfg.ZTPNetwork)
			if err := ztpServer.Start(ctx); err != nil {
				errCh <- fmt.Errorf("ZTP server error: %w", err)
			}
		}()
	}

	// Start epoch-based allocation reaper (Demo F - WiFi mode)
	// Uses epoch-based expiration: advances epoch periodically, then reclaims
	// allocations where Epoch < currentEpoch - gracePeriod (default: 2 epochs)
	go func() {
		// Epoch advances every hour by default (configurable via allocStore.SetEpochPeriod)
		ticker := time.NewTicker(allocStore.GetEpochPeriod())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Advance epoch first
				newEpoch := allocStore.AdvanceEpoch()
				fmt.Printf("Epoch: advanced to %d\n", newEpoch)

				// Then clean up expired allocations (epoch < currentEpoch - gracePeriod)
				deleted, err := allocStore.CleanupExpiredAllocations(ctx)
				if err != nil {
					fmt.Printf("Reaper: error cleaning expired allocations: %v\n", err)
				} else if deleted > 0 {
					fmt.Printf("Reaper: cleaned up %d expired allocations (epoch threshold: %d)\n",
						deleted, newEpoch-allocStore.GetGracePeriod())
				}
			}
		}
	}()

	fmt.Println("Nexus ready!")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
	case err := <-errCh:
		fmt.Printf("Server error: %v\n", err)
	case <-ctx.Done():
	}

	// Graceful shutdown with peer acknowledgment (P2P mode)
	if stateManager != nil {
		fmt.Println("Performing graceful shutdown with peer acknowledgment...")
		shutdownOpts := &state.GracefulShutdownOptions{
			MaxWaitTime:   10 * time.Second,
			GracePeriod:   2 * time.Second,
			CheckInterval: 500 * time.Millisecond,
			PeerSelector: func(metadata map[string]string) bool {
				// Only wait for acknowledgment from Nexus write nodes
				return metadata["name"] == "Nexus" && metadata["role"] != "read"
			},
			RequiredMatches: 1,
		}

		gracefulCtx, gracefulCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := stateManager.GracefulShutdown(gracefulCtx, shutdownOpts); err != nil {
			fmt.Printf("Graceful shutdown failed (proceeding anyway): %v\n", err)
		}
		gracefulCancel()

		if err := stateManager.Close(); err != nil {
			fmt.Printf("Failed to close P2P host: %v\n", err)
		}
	}

	// Shutdown HTTP servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	httpServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)

	if grpcServer != nil {
		grpcServer.GracefulStop()
	}

	if ztpServer != nil {
		ztpServer.Stop()
	}

	fmt.Println("Nexus stopped")
	return nil
}

// parseZTPConfig parses ZTP configuration from flags.
func parseZTPConfig(cfg Config) (ztp.Config, error) {
	_, network, err := net.ParseCIDR(cfg.ZTPNetwork)
	if err != nil {
		return ztp.Config{}, fmt.Errorf("invalid ZTP network %s: %w", cfg.ZTPNetwork, err)
	}

	// Parse gateway (default to first usable IP in network)
	var gateway net.IP
	if cfg.ZTPGateway != "" {
		gateway = net.ParseIP(cfg.ZTPGateway)
		if gateway == nil {
			return ztp.Config{}, fmt.Errorf("invalid ZTP gateway %s", cfg.ZTPGateway)
		}
	} else {
		// Default gateway is first IP + 1 (skip network address)
		gateway = make(net.IP, 4)
		copy(gateway, network.IP.To4())
		gateway[3]++
	}

	// Parse DNS servers
	var dnsServers []net.IP
	if cfg.ZTPDNS != "" {
		for _, dnsStr := range strings.Split(cfg.ZTPDNS, ",") {
			dnsStr = strings.TrimSpace(dnsStr)
			dns := net.ParseIP(dnsStr)
			if dns == nil {
				return ztp.Config{}, fmt.Errorf("invalid DNS server %s", dnsStr)
			}
			dnsServers = append(dnsServers, dns)
		}
	}

	// Get server IP from interface for Nexus URL
	iface, err := net.InterfaceByName(cfg.ZTPInterface)
	if err != nil {
		return ztp.Config{}, fmt.Errorf("failed to get interface %s: %w", cfg.ZTPInterface, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return ztp.Config{}, fmt.Errorf("failed to get interface addresses: %w", err)
	}

	var serverIP net.IP
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			serverIP = ipnet.IP.To4()
			break
		}
	}
	if serverIP == nil {
		return ztp.Config{}, fmt.Errorf("no IPv4 address found on interface %s", cfg.ZTPInterface)
	}

	// Build Nexus URL from server IP and HTTP port
	nexusURL := fmt.Sprintf("http://%s:%d", serverIP, cfg.HTTPPort)

	return ztp.Config{
		Interface: cfg.ZTPInterface,
		Network:   *network,
		Gateway:   gateway,
		DNS:       dnsServers,
		LeaseTime: 24 * time.Hour,
		NexusURL:  nexusURL,
	}, nil
}

// loadOrGenerateKey loads an existing private key or generates a new one.
func loadOrGenerateKey(dataPath string) (crypto.PrivKey, error) {
	keyPath := dataPath + "/node.key"

	// Try to load existing key
	keyData, err := os.ReadFile(keyPath)
	if err == nil {
		pk, err := crypto.UnmarshalPrivateKey(keyData)
		if err == nil {
			return pk, nil
		}
	}

	// Generate new key
	pk, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Save key
	keyData, err = crypto.MarshalPrivateKey(pk)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		return nil, fmt.Errorf("failed to save private key: %w", err)
	}

	return pk, nil
}

// memoryStateStore is a simple in-memory state store for standalone mode.
type memoryStateStore struct {
	members map[string]*store.NodeMember
}

func (m *memoryStateStore) GetMembers(ctx context.Context) map[string]*store.NodeMember {
	if m.members == nil {
		m.members = make(map[string]*store.NodeMember)
	}
	return m.members
}

// runRendezvousServer starts a libp2p rendezvous server for peer discovery.
// dbBackend can be "memory" (default, no CGO required) or "badger" (persistent).
func runRendezvousServer(port int, dataPath string, dbBackend string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Printf("Starting Nexus Rendezvous Server\n")
	fmt.Printf("  Port:     %d\n", port)
	fmt.Printf("  Data:     %s\n", dataPath)
	fmt.Printf("  Backend:  %s\n", dbBackend)

	// Create data directory
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Load or generate key
	pk, err := loadOrGenerateKey(dataPath)
	if err != nil {
		return fmt.Errorf("failed to load/generate key: %w", err)
	}

	// Create libp2p host
	h, err := createRendezvousHost(pk, port)
	if err != nil {
		return fmt.Errorf("failed to create libp2p host: %w", err)
	}
	defer h.Close()

	// Create database backend (pure Go, no CGO required)
	var rvzDB dbi.DB
	switch dbBackend {
	case "badger":
		dbPath := dataPath + "/rendezvous-badger"
		db, err := rendezvousdb.NewBadgerDB(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open badger database: %w", err)
		}
		defer db.Close()
		rvzDB = db
	default: // "memory"
		rvzDB = rendezvousdb.NewMemoryDB()
	}

	// Create and start rendezvous service
	_ = rendezvous.NewRendezvousService(h, rvzDB)

	// Print server info
	for _, addr := range h.Addrs() {
		fmt.Printf("  Listening: %s/p2p/%s\n", addr, h.ID())
	}
	fmt.Printf("\nRendezvous server ready!\n")
	fmt.Printf("Connect nodes with: --rendezvous-server=%s/p2p/%s\n", h.Addrs()[0], h.ID())

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
	case <-ctx.Done():
	}

	fmt.Println("Rendezvous server stopped")
	return nil
}

// createRendezvousHost creates a minimal libp2p host for the rendezvous server.
func createRendezvousHost(pk crypto.PrivKey, port int) (libp2pHost.Host, error) {
	return libp2p.New(
		libp2p.Identity(pk),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)),
	)
}
