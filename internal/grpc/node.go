package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	nexusv1 "github.com/codelaboratoryltd/nexus/api/proto/nexus/v1"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// NodeService implements the gRPC NodeService.
type NodeService struct {
	nexusv1.UnimplementedNodeServiceServer
	nodeStore store.NodeStore
}

// NewNodeService creates a new NodeService.
func NewNodeService(nodeStore store.NodeStore) *NodeService {
	return &NodeService{
		nodeStore: nodeStore,
	}
}

// Register registers a node or updates its heartbeat.
// Note: The current NodeStore is read-only (backed by CLSet CRDT state).
// Registration happens via P2P membership. This RPC returns the current
// state of the node if it exists.
func (s *NodeService) Register(ctx context.Context, req *nexusv1.RegisterRequest) (*nexusv1.RegisterResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	// Look up the node in the store
	nodes, err := s.nodeStore.ListNodes(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list nodes: %v", err)
	}

	for _, n := range nodes {
		if n.ID == req.NodeId {
			return &nexusv1.RegisterResponse{
				Node: nodeToProto(n),
			}, nil
		}
	}

	return nil, status.Errorf(codes.NotFound, "node %s not found in cluster state", req.NodeId)
}

// Heartbeat sends a heartbeat for a node.
func (s *NodeService) Heartbeat(ctx context.Context, req *nexusv1.HeartbeatRequest) (*nexusv1.HeartbeatResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	nodes, err := s.nodeStore.ListNodes(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list nodes: %v", err)
	}

	for _, n := range nodes {
		if n.ID == req.NodeId {
			return &nexusv1.HeartbeatResponse{
				Node: nodeToProto(n),
			}, nil
		}
	}

	return nil, status.Errorf(codes.NotFound, "node %s not found", req.NodeId)
}

// ListNodes returns all cluster nodes.
func (s *NodeService) ListNodes(ctx context.Context, req *nexusv1.ListNodesRequest) (*nexusv1.ListNodesResponse, error) {
	nodes, err := s.nodeStore.ListNodes(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list nodes: %v", err)
	}

	protoNodes := make([]*nexusv1.Node, 0, len(nodes))
	for _, n := range nodes {
		protoNodes = append(protoNodes, nodeToProto(n))
	}

	return &nexusv1.ListNodesResponse{
		Nodes: protoNodes,
		Count: int32(len(protoNodes)),
	}, nil
}

// nodeToProto converts a store.Node to proto Node.
func nodeToProto(n *store.Node) *nexusv1.Node {
	nodeStatus := "active"
	if time.Now().After(n.BestBefore) {
		nodeStatus = "expired"
	}

	return &nexusv1.Node{
		Id:         n.ID,
		BestBefore: timestamppb.New(n.BestBefore),
		Metadata:   n.Metadata,
		Status:     nodeStatus,
	}
}
