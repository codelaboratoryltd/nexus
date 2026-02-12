package grpc

import (
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	nexusv1 "github.com/codelaboratoryltd/nexus/api/proto/nexus/v1"
)

// WatchService implements the gRPC WatchService with server-streaming.
type WatchService struct {
	nexusv1.UnimplementedWatchServiceServer

	mu          sync.RWMutex
	subscribers map[uint64]chan *nexusv1.WatchEvent
	nextID      uint64
}

// NewWatchService creates a new WatchService.
func NewWatchService() *WatchService {
	return &WatchService{
		subscribers: make(map[uint64]chan *nexusv1.WatchEvent),
	}
}

// Watch streams configuration changes to the client.
func (s *WatchService) Watch(req *nexusv1.WatchRequest, stream nexusv1.WatchService_WatchServer) error {
	ch := make(chan *nexusv1.WatchEvent, 64)

	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subscribers[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event := <-ch:
			if !matchesFilter(req, event) {
				continue
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

// Emit broadcasts an event to all connected watchers.
func (s *WatchService) Emit(event *nexusv1.WatchEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// Drop event if subscriber is too slow
		}
	}
}

// EmitPoolCreated emits a pool-created event.
func (s *WatchService) EmitPoolCreated(poolID, cidr string) {
	s.Emit(&nexusv1.WatchEvent{
		Type:       nexusv1.EventType_EVENT_TYPE_POOL_CREATED,
		Timestamp:  timestamppb.Now(),
		ResourceId: poolID,
		Payload: &nexusv1.WatchEvent_Pool{
			Pool: &nexusv1.PoolEvent{
				Id:   poolID,
				Cidr: cidr,
			},
		},
	})
}

// EmitPoolDeleted emits a pool-deleted event.
func (s *WatchService) EmitPoolDeleted(poolID string) {
	s.Emit(&nexusv1.WatchEvent{
		Type:       nexusv1.EventType_EVENT_TYPE_POOL_DELETED,
		Timestamp:  timestamppb.Now(),
		ResourceId: poolID,
		Payload: &nexusv1.WatchEvent_Pool{
			Pool: &nexusv1.PoolEvent{
				Id: poolID,
			},
		},
	})
}

// EmitAllocationCreated emits an allocation-created event.
func (s *WatchService) EmitAllocationCreated(poolID, subscriberID, ip, nodeID string) {
	s.Emit(&nexusv1.WatchEvent{
		Type:       nexusv1.EventType_EVENT_TYPE_ALLOCATION_CREATED,
		Timestamp:  timestamppb.Now(),
		ResourceId: subscriberID,
		Payload: &nexusv1.WatchEvent_Allocation{
			Allocation: &nexusv1.AllocationEvent{
				PoolId:       poolID,
				SubscriberId: subscriberID,
				Ip:           ip,
				NodeId:       nodeID,
			},
		},
	})
}

// EmitAllocationReleased emits an allocation-released event.
func (s *WatchService) EmitAllocationReleased(poolID, subscriberID string) {
	s.Emit(&nexusv1.WatchEvent{
		Type:       nexusv1.EventType_EVENT_TYPE_ALLOCATION_RELEASED,
		Timestamp:  timestamppb.Now(),
		ResourceId: subscriberID,
		Payload: &nexusv1.WatchEvent_Allocation{
			Allocation: &nexusv1.AllocationEvent{
				PoolId:       poolID,
				SubscriberId: subscriberID,
			},
		},
	})
}

// matchesFilter checks if an event matches the watch request filters.
func matchesFilter(req *nexusv1.WatchRequest, event *nexusv1.WatchEvent) bool {
	// Filter by resource type
	if len(req.ResourceTypes) > 0 {
		typeName := event.Type.String()
		matched := false
		for _, rt := range req.ResourceTypes {
			if rt == typeName {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Filter by pool ID
	if req.PoolId != "" {
		switch p := event.Payload.(type) {
		case *nexusv1.WatchEvent_Pool:
			if p.Pool.Id != req.PoolId {
				return false
			}
		case *nexusv1.WatchEvent_Allocation:
			if p.Allocation.PoolId != req.PoolId {
				return false
			}
		}
	}

	return true
}
