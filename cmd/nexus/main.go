package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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

	fmt.Println("Nexus stopped")
	return nil
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
