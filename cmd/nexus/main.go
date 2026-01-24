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
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/codelaboratoryltd/nexus/internal/api"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/keys"
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

	// ZTP DHCP server config
	ZTPEnabled   bool
	ZTPInterface string
	ZTPNetwork   string
	ZTPGateway   string
	ZTPDNS       string
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

	// ZTP DHCP server flags
	serve.Flags().BoolVar(&cfg.ZTPEnabled, "ztp", false, "Enable ZTP DHCP server for OLT-BNG provisioning")
	serve.Flags().StringVar(&cfg.ZTPInterface, "ztp-interface", "eth0", "Interface for ZTP DHCP server")
	serve.Flags().StringVar(&cfg.ZTPNetwork, "ztp-network", "192.168.100.0/24", "Management network CIDR for OLT-BNG devices")
	serve.Flags().StringVar(&cfg.ZTPGateway, "ztp-gateway", "", "Gateway IP for management network (defaults to first IP)")
	serve.Flags().StringVar(&cfg.ZTPDNS, "ztp-dns", "8.8.8.8,8.8.4.4", "DNS servers (comma-separated)")

	version := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Nexus %s (%s)\n", BuildVersion, BuildCommit)
		},
	}

	root.AddCommand(serve, version)
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
	fmt.Printf("  ZTP:      %v\n", cfg.ZTPEnabled)

	var (
		ring       *hashring.VirtualHashRing
		poolStore  store.PoolStore
		nodeStore  store.NodeStore
		allocStore store.AllocationStore
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
			PrivateKey:     pk,
			ListenPort:     uint32(cfg.P2PPort),
			Role:           cfg.Role,
			Topic:          "nexus-state",
			Bootstrap:      cfg.Bootstrap,
			Datastore:      bds,
			EventQueueSize: 1000,
		}

		stateManager, err := state.NewStateManager(ctx, stateCfg)
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

	// Create HTTP router
	router := mux.NewRouter()
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
	errCh := make(chan error, 2)

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

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	httpServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)

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
