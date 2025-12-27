package store

import (
	"context"
	"time"
)

// nodeStore manages node data from the distributed state.
type nodeStore struct {
	state StateStore
}

// NewNodeStore creates a new node store with the given state store.
func NewNodeStore(state StateStore) NodeStore {
	return &nodeStore{
		state: state,
	}
}

// ListNodes retrieves all nodes from the distributed state.
func (ns *nodeStore) ListNodes(ctx context.Context) ([]*Node, error) {
	members := ns.state.GetMembers(ctx)
	result := make([]*Node, 0, len(members))

	for k, v := range members {
		result = append(result, &Node{
			ID:         k,
			BestBefore: time.Unix(int64(v.BestBefore), 0),
			Metadata:   v.Metadata,
		})
	}
	return result, nil
}
