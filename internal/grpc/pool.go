package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nexusv1 "github.com/codelaboratoryltd/nexus/api/proto/nexus/v1"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/validation"
)

// PoolService implements the gRPC PoolService.
type PoolService struct {
	nexusv1.UnimplementedPoolServiceServer
	ring      *hashring.VirtualHashRing
	poolStore store.PoolStore
}

// NewPoolService creates a new PoolService.
func NewPoolService(ring *hashring.VirtualHashRing, poolStore store.PoolStore) *PoolService {
	return &PoolService{
		ring:      ring,
		poolStore: poolStore,
	}
}

// CreatePool creates a new IP address pool.
func (s *PoolService) CreatePool(ctx context.Context, req *nexusv1.CreatePoolRequest) (*nexusv1.CreatePoolResponse, error) {
	if err := validation.ValidatePoolID(req.Id); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	ipNet, err := validation.ValidateCIDR(req.Cidr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if req.Prefix > 0 {
		if err := validation.ValidatePrefixForCIDR(ipNet, int(req.Prefix)); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	if err := validation.ValidateShardingFactor(int(req.ShardingFactor)); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateExclusions(req.Exclusions); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateMetadata(req.Metadata); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateBackupRatio(req.BackupRatio); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateGateway(req.Gateway); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateDNS(req.Dns); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	pool := &store.Pool{
		ID:             req.Id,
		CIDR:           *ipNet,
		Prefix:         int(req.Prefix),
		Exclusions:     req.Exclusions,
		Metadata:       req.Metadata,
		ShardingFactor: int(req.ShardingFactor),
		BackupRatio:    req.BackupRatio,
		Gateway:        req.Gateway,
		DNS:            req.Dns,
	}

	if err := pool.Validate(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := s.poolStore.SavePool(ctx, pool); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save pool: %v", err)
	}

	s.ring.AddPool(req.Id, ipNet)

	return &nexusv1.CreatePoolResponse{
		Pool: poolToProto(pool),
	}, nil
}

// GetPool returns a specific pool by ID.
func (s *PoolService) GetPool(ctx context.Context, req *nexusv1.GetPoolRequest) (*nexusv1.GetPoolResponse, error) {
	if err := validation.ValidatePoolID(req.Id); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	pool, err := s.poolStore.GetPool(ctx, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "pool not found")
	}

	return &nexusv1.GetPoolResponse{
		Pool: poolToProto(pool),
	}, nil
}

// ListPools returns all pools.
func (s *PoolService) ListPools(ctx context.Context, req *nexusv1.ListPoolsRequest) (*nexusv1.ListPoolsResponse, error) {
	pools, err := s.poolStore.ListPools(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list pools: %v", err)
	}

	protoPools := make([]*nexusv1.Pool, 0, len(pools))
	for _, p := range pools {
		protoPools = append(protoPools, poolToProto(p))
	}

	return &nexusv1.ListPoolsResponse{
		Pools: protoPools,
		Count: int32(len(protoPools)),
	}, nil
}

// DeletePool removes a pool.
func (s *PoolService) DeletePool(ctx context.Context, req *nexusv1.DeletePoolRequest) (*nexusv1.DeletePoolResponse, error) {
	if err := validation.ValidatePoolID(req.Id); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := s.poolStore.DeletePool(ctx, req.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete pool: %v", err)
	}

	s.ring.RemovePool(req.Id)

	return &nexusv1.DeletePoolResponse{}, nil
}

// poolToProto converts a store.Pool to proto Pool.
func poolToProto(p *store.Pool) *nexusv1.Pool {
	return &nexusv1.Pool{
		Id:             p.ID,
		Cidr:           p.CIDR.String(),
		Prefix:         int32(p.Prefix),
		Exclusions:     p.Exclusions,
		Metadata:       p.Metadata,
		ShardingFactor: int32(p.ShardingFactor),
		BackupRatio:    p.BackupRatio,
		Gateway:        p.Gateway,
		Dns:            p.DNS,
	}
}
