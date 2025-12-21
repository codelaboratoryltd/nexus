package store

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// MemoryStore implements Store with in-memory storage
type MemoryStore struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber
	macIndex    map[string]string // MAC -> subscriber ID
	olts        map[string]*OLT
	pools       map[string]*IPPool
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		subscribers: make(map[string]*Subscriber),
		macIndex:    make(map[string]string),
		olts:        make(map[string]*OLT),
		pools:       make(map[string]*IPPool),
	}
}

// GetSubscriber returns a subscriber by ID
func (m *MemoryStore) GetSubscriber(ctx context.Context, id string) (*Subscriber, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sub, ok := m.subscribers[id]
	if !ok {
		return nil, fmt.Errorf("subscriber not found: %s", id)
	}
	return sub, nil
}

// GetSubscriberByMAC returns a subscriber by MAC address
func (m *MemoryStore) GetSubscriberByMAC(ctx context.Context, mac net.HardwareAddr) (*Subscriber, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.macIndex[mac.String()]
	if !ok {
		return nil, fmt.Errorf("subscriber not found for MAC: %s", mac)
	}
	return m.subscribers[id], nil
}

// ListSubscribers returns all subscribers, optionally filtered by OLT
func (m *MemoryStore) ListSubscribers(ctx context.Context, oltID string) ([]*Subscriber, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Subscriber
	for _, sub := range m.subscribers {
		if oltID == "" || sub.OLTID == oltID {
			result = append(result, sub)
		}
	}
	return result, nil
}

// CreateSubscriber creates a new subscriber
func (m *MemoryStore) CreateSubscriber(ctx context.Context, sub *Subscriber) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.subscribers[sub.ID]; exists {
		return fmt.Errorf("subscriber already exists: %s", sub.ID)
	}

	m.subscribers[sub.ID] = sub
	m.macIndex[sub.MAC.String()] = sub.ID
	return nil
}

// UpdateSubscriber updates an existing subscriber
func (m *MemoryStore) UpdateSubscriber(ctx context.Context, sub *Subscriber) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.subscribers[sub.ID]; !exists {
		return fmt.Errorf("subscriber not found: %s", sub.ID)
	}

	m.subscribers[sub.ID] = sub
	m.macIndex[sub.MAC.String()] = sub.ID
	return nil
}

// DeleteSubscriber removes a subscriber
func (m *MemoryStore) DeleteSubscriber(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscribers[id]
	if !exists {
		return fmt.Errorf("subscriber not found: %s", id)
	}

	delete(m.macIndex, sub.MAC.String())
	delete(m.subscribers, id)
	return nil
}

// GetOLT returns an OLT by ID
func (m *MemoryStore) GetOLT(ctx context.Context, id string) (*OLT, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	olt, ok := m.olts[id]
	if !ok {
		return nil, fmt.Errorf("OLT not found: %s", id)
	}
	return olt, nil
}

// ListOLTs returns all registered OLTs
func (m *MemoryStore) ListOLTs(ctx context.Context) ([]*OLT, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*OLT
	for _, olt := range m.olts {
		result = append(result, olt)
	}
	return result, nil
}

// RegisterOLT registers a new OLT
func (m *MemoryStore) RegisterOLT(ctx context.Context, olt *OLT) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.olts[olt.ID] = olt
	return nil
}

// UpdateOLT updates an OLT record
func (m *MemoryStore) UpdateOLT(ctx context.Context, olt *OLT) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.olts[olt.ID]; !exists {
		return fmt.Errorf("OLT not found: %s", olt.ID)
	}

	m.olts[olt.ID] = olt
	return nil
}

// DeregisterOLT removes an OLT
func (m *MemoryStore) DeregisterOLT(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.olts, id)
	return nil
}

// GetPool returns an IP pool by ID
func (m *MemoryStore) GetPool(ctx context.Context, id string) (*IPPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, ok := m.pools[id]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", id)
	}
	return pool, nil
}

// ListPools returns all IP pools
func (m *MemoryStore) ListPools(ctx context.Context) ([]*IPPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*IPPool
	for _, pool := range m.pools {
		result = append(result, pool)
	}
	return result, nil
}

// Ping checks store health
func (m *MemoryStore) Ping(ctx context.Context) error {
	return nil
}

// Close closes the store
func (m *MemoryStore) Close() error {
	return nil
}

// Ensure MemoryStore implements Store
var _ Store = (*MemoryStore)(nil)
