package grpc

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	nexusv1 "github.com/codelaboratoryltd/nexus/api/proto/nexus/v1"
	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/validation"
)

// AllocationService implements the gRPC AllocationService.
type AllocationService struct {
	nexusv1.UnimplementedAllocationServiceServer
	ring       *hashring.VirtualHashRing
	poolStore  store.PoolStore
	allocStore store.AllocationStore
}

// NewAllocationService creates a new AllocationService.
func NewAllocationService(ring *hashring.VirtualHashRing, poolStore store.PoolStore, allocStore store.AllocationStore) *AllocationService {
	return &AllocationService{
		ring:       ring,
		poolStore:  poolStore,
		allocStore: allocStore,
	}
}

// Allocate creates a new IP allocation for a subscriber.
func (s *AllocationService) Allocate(ctx context.Context, req *nexusv1.AllocateRequest) (*nexusv1.AllocateResponse, error) {
	if err := validation.ValidatePoolID(req.PoolId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if err := validation.ValidateSubscriberID(req.SubscriberId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	if req.NodeId != "" {
		if err := validation.ValidateNodeID(req.NodeId); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	// Check for existing allocation
	existing, err := s.allocStore.GetAllocationBySubscriber(ctx, req.SubscriberId)
	if err == nil && existing != nil {
		return nil, status.Error(codes.AlreadyExists, "subscriber already has an allocation")
	}

	var ip net.IP
	if req.Ip != "" {
		parsedIP, err := validation.ValidateIPAddress(req.Ip)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		ip = parsedIP
	} else {
		allocatedIP := s.ring.AllocateIP(req.PoolId, req.SubscriberId)
		if allocatedIP == nil {
			return nil, status.Error(codes.ResourceExhausted, "no IPs available in pool")
		}
		ip = allocatedIP
	}

	now := time.Now()
	allocation := &store.Allocation{
		PoolID:       req.PoolId,
		SubscriberID: req.SubscriberId,
		IP:           ip,
		Timestamp:    now,
		NodeID:       req.NodeId,
	}

	if req.Ttl > 0 {
		allocation.TTL = req.Ttl
		allocation.Epoch = s.allocStore.GetCurrentEpoch()
		allocation.LastRenewed = now
		epochPeriod := s.allocStore.GetEpochPeriod()
		gracePeriod := s.allocStore.GetGracePeriod()
		allocation.ExpiresAt = now.Add(time.Duration(gracePeriod) * epochPeriod)
	}
	if req.AllocType != "" {
		allocation.AllocType = store.AllocationType(req.AllocType)
	}

	if err := s.allocStore.SaveAllocation(ctx, allocation); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save allocation: %v", err)
	}

	return &nexusv1.AllocateResponse{
		Allocation: allocationToProto(allocation),
	}, nil
}

// Release removes a subscriber's IP allocation.
func (s *AllocationService) Release(ctx context.Context, req *nexusv1.ReleaseRequest) (*nexusv1.ReleaseResponse, error) {
	if err := validation.ValidateSubscriberID(req.SubscriberId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	poolID := req.PoolId
	if poolID != "" {
		if err := validation.ValidatePoolID(poolID); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	if poolID == "" {
		allocation, err := s.allocStore.GetAllocationBySubscriber(ctx, req.SubscriberId)
		if err != nil {
			return nil, status.Error(codes.NotFound, "allocation not found")
		}
		poolID = allocation.PoolID
	}

	if err := s.allocStore.RemoveAllocation(ctx, poolID, req.SubscriberId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove allocation: %v", err)
	}

	return &nexusv1.ReleaseResponse{}, nil
}

// Get returns a subscriber's current allocation.
func (s *AllocationService) Get(ctx context.Context, req *nexusv1.GetAllocationRequest) (*nexusv1.GetAllocationResponse, error) {
	if err := validation.ValidateSubscriberID(req.SubscriberId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	allocation, err := s.allocStore.GetAllocationBySubscriber(ctx, req.SubscriberId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "allocation not found")
	}

	return &nexusv1.GetAllocationResponse{
		Allocation: allocationToProto(allocation),
	}, nil
}

// List returns allocations filtered by pool ID.
func (s *AllocationService) List(ctx context.Context, req *nexusv1.ListAllocationsRequest) (*nexusv1.ListAllocationsResponse, error) {
	if req.PoolId == "" {
		return nil, status.Error(codes.InvalidArgument, "pool_id is required")
	}

	if err := validation.ValidatePoolID(req.PoolId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	allocations, err := s.allocStore.ListAllocationsByPool(ctx, req.PoolId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list allocations: %v", err)
	}

	protoAllocs := make([]*nexusv1.Allocation, 0, len(allocations))
	for _, a := range allocations {
		protoAllocs = append(protoAllocs, allocationToProto(a))
	}

	return &nexusv1.ListAllocationsResponse{
		Allocations: protoAllocs,
		Count:       int32(len(protoAllocs)),
	}, nil
}

// allocationToProto converts a store.Allocation to proto Allocation.
func allocationToProto(a *store.Allocation) *nexusv1.Allocation {
	alloc := &nexusv1.Allocation{
		PoolId:       a.PoolID,
		SubscriberId: a.SubscriberID,
		Ip:           a.IP.String(),
		Timestamp:    timestamppb.New(a.Timestamp),
		NodeId:       a.NodeID,
		BackupNodeId: a.BackupNodeID,
		IsBackup:     a.IsBackup,
		Ttl:          a.TTL,
		Epoch:        a.Epoch,
		AllocType:    string(a.AllocType),
	}

	if !a.ExpiresAt.IsZero() {
		alloc.ExpiresAt = timestamppb.New(a.ExpiresAt)
	}
	if !a.LastRenewed.IsZero() {
		alloc.LastRenewed = timestamppb.New(a.LastRenewed)
	}

	return alloc
}
