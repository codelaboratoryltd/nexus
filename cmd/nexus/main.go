package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
)

var (
	httpPort    int
	grpcPort    int
	metricsPort int
	storeType   string
	etcdAddr    string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nexus",
		Short: "Nexus - Central coordination service for BNG edge network",
		Long: `Nexus provides central coordination for distributed OLT-BNG deployments:
  - Bootstrap API for OLT registration
  - Subscriber state management (CLSet CRDT)
  - IP allocation via consistent hashring
  - Configuration distribution`,
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Nexus server",
		RunE:  runServe,
	}

	serveCmd.Flags().IntVar(&httpPort, "http-port", 9000, "HTTP API port")
	serveCmd.Flags().IntVar(&grpcPort, "grpc-port", 9001, "gRPC API port")
	serveCmd.Flags().IntVar(&metricsPort, "metrics-port", 9002, "Prometheus metrics port")
	serveCmd.Flags().StringVar(&storeType, "store", "memory", "Store type: memory, etcd")
	serveCmd.Flags().StringVar(&etcdAddr, "etcd-addr", "localhost:2379", "etcd address")

	rootCmd.AddCommand(serveCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", healthHandler)
	httpMux.HandleFunc("/ready", readyHandler)
	httpMux.HandleFunc("/api/v1/bootstrap", bootstrapHandler)
	httpMux.HandleFunc("/api/v1/subscribers", subscribersHandler)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: httpMux,
	}

	// Start metrics server
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", metricsPort),
		Handler: metricsMux,
	}

	// Start gRPC server
	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC port: %w", err)
	}

	log.Printf("Nexus starting...")
	log.Printf("  HTTP API:  http://localhost:%d", httpPort)
	log.Printf("  gRPC API:  localhost:%d", grpcPort)
	log.Printf("  Metrics:   http://localhost:%d/metrics", metricsPort)
	log.Printf("  Store:     %s", storeType)

	// Run servers
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

	go func() {
		// gRPC server placeholder
		log.Printf("gRPC server listening on %s", grpcListener.Addr())
		// TODO: Register gRPC services
		<-ctx.Done()
	}()

	// Wait for shutdown or error
	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("Server error: %v", err)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	httpServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)
	grpcListener.Close()

	log.Printf("Nexus stopped")
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Check store connectivity
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}

func bootstrapHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement OLT bootstrap/registration
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "not_implemented"}`))
}

func subscribersHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement subscriber CRUD
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"subscribers": []}`))
}
