package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
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
	fmt.Printf("  P2P:      :%d\n", cfg.P2PPort)

	// Initialize hashring
	ring := hashring.NewVirtualNodesHashRing()

	// Create HTTP router
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/ready", readyHandler).Methods("GET")

	// API v1 endpoints
	api := router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/pools", listPoolsHandler(ring)).Methods("GET")
	api.HandleFunc("/pools", createPoolHandler(ring)).Methods("POST")
	api.HandleFunc("/nodes", listNodesHandler(ring)).Methods("GET")

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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}

func listPoolsHandler(ring *hashring.VirtualHashRing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pools := ring.GetIPPools()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"pools": %d}`, len(pools))
	}
}

func createPoolHandler(ring *hashring.VirtualHashRing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: Parse request body and create pool
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"error": "not implemented"}`))
	}
}

func listNodesHandler(ring *hashring.VirtualHashRing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes := ring.ListNodes()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"nodes": %d}`, len(nodes))
	}
}
