package grpc

import (
	"crypto/tls"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	nexusv1 "github.com/codelaboratoryltd/nexus/api/proto/nexus/v1"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// ServerConfig holds gRPC server configuration.
type ServerConfig struct {
	Port    int
	TLSCert string
	TLSKey  string
}

// Server wraps a gRPC server with Nexus service implementations.
type Server struct {
	grpcServer *grpc.Server
	config     ServerConfig

	nodeSvc       *NodeService
	poolSvc       *PoolService
	allocationSvc *AllocationService
	watchSvc      *WatchService
}

// NewServer creates a new gRPC server with all service implementations.
func NewServer(cfg ServerConfig, ring *hashring.VirtualHashRing, poolStore store.PoolStore, nodeStore store.NodeStore, allocStore store.AllocationStore) (*Server, error) {
	var opts []grpc.ServerOption

	// Configure TLS if cert and key are provided
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	grpcServer := grpc.NewServer(opts...)

	nodeSvc := NewNodeService(nodeStore)
	poolSvc := NewPoolService(ring, poolStore)
	allocationSvc := NewAllocationService(ring, poolStore, allocStore)
	watchSvc := NewWatchService()

	nexusv1.RegisterNodeServiceServer(grpcServer, nodeSvc)
	nexusv1.RegisterPoolServiceServer(grpcServer, poolSvc)
	nexusv1.RegisterAllocationServiceServer(grpcServer, allocationSvc)
	nexusv1.RegisterWatchServiceServer(grpcServer, watchSvc)

	// Enable server reflection for grpcurl and debugging
	reflection.Register(grpcServer)

	return &Server{
		grpcServer:    grpcServer,
		config:        cfg,
		nodeSvc:       nodeSvc,
		poolSvc:       poolSvc,
		allocationSvc: allocationSvc,
		watchSvc:      watchSvc,
	}, nil
}

// Serve starts the gRPC server on the configured port.
func (s *Server) Serve() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.config.Port, err)
	}
	return s.grpcServer.Serve(lis)
}

// GracefulStop stops the gRPC server gracefully.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}

// WatchService returns the watch service for emitting events.
func (s *Server) WatchService() *WatchService {
	return s.watchSvc
}
