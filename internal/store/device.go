package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// DeviceStatus represents the registration status of a device.
type DeviceStatus string

const (
	// DeviceStatusPending indicates the device is awaiting configuration.
	DeviceStatusPending DeviceStatus = "pending"
	// DeviceStatusConfigured indicates the device has been assigned a site and configuration.
	DeviceStatusConfigured DeviceStatus = "configured"
)

// DeviceRole represents the role of a device in an HA pair.
type DeviceRole string

const (
	// DeviceRoleActive indicates the device is the active/primary device.
	DeviceRoleActive DeviceRole = "active"
	// DeviceRoleStandby indicates the device is the standby/backup device.
	DeviceRoleStandby DeviceRole = "standby"
)

// Device represents an OLT-BNG device registered with Nexus.
type Device struct {
	// NodeID is the unique identifier assigned by Nexus.
	NodeID string `json:"node_id" cbor:"node_id"`

	// Serial is the hardware serial number.
	Serial string `json:"serial" cbor:"serial"`

	// MAC is the primary MAC address.
	MAC string `json:"mac" cbor:"mac"`

	// Model is the device model identifier.
	Model string `json:"model,omitempty" cbor:"model"`

	// Firmware is the current firmware version.
	Firmware string `json:"firmware,omitempty" cbor:"firmware"`

	// PublicKey is the device's public key for mTLS (future use).
	PublicKey string `json:"public_key,omitempty" cbor:"public_key"`

	// Status is the current registration status.
	Status DeviceStatus `json:"status" cbor:"status"`

	// SiteID is the assigned site identifier (if configured).
	SiteID string `json:"site_id,omitempty" cbor:"site_id"`

	// Role is the device role in an HA pair (if configured).
	Role DeviceRole `json:"role,omitempty" cbor:"role"`

	// PartnerNodeID is the node ID of the HA partner (if configured).
	PartnerNodeID string `json:"partner_node_id,omitempty" cbor:"partner_node_id"`

	// AssignedPools is the list of pool IDs assigned to this device.
	AssignedPools []string `json:"assigned_pools,omitempty" cbor:"assigned_pools"`

	// FirstSeen is when the device was first registered.
	FirstSeen time.Time `json:"first_seen" cbor:"first_seen"`

	// LastSeen is when the device last bootstrapped.
	LastSeen time.Time `json:"last_seen" cbor:"last_seen"`

	// Metadata holds additional device metadata.
	Metadata map[string]string `json:"metadata,omitempty" cbor:"metadata"`
}

// DeviceStore manages device registrations.
type DeviceStore interface {
	// SaveDevice persists a device registration.
	SaveDevice(ctx context.Context, device *Device) error

	// GetDevice retrieves a device by node ID.
	GetDevice(ctx context.Context, nodeID string) (*Device, error)

	// GetDeviceBySerial retrieves a device by serial number.
	GetDeviceBySerial(ctx context.Context, serial string) (*Device, error)

	// GetDeviceByMAC retrieves a device by MAC address.
	GetDeviceByMAC(ctx context.Context, mac string) (*Device, error)

	// ListDevices retrieves all registered devices.
	ListDevices(ctx context.Context) ([]*Device, error)

	// ListDevicesBySite retrieves all devices for a given site.
	ListDevicesBySite(ctx context.Context, siteID string) ([]*Device, error)

	// DeleteDevice removes a device registration.
	DeleteDevice(ctx context.Context, nodeID string) error
}

// Errors for device operations.
var (
	ErrDeviceNotFound  = fmt.Errorf("device not found")
	ErrDuplicateDevice = fmt.Errorf("device already exists")
)

// deviceStore is the datastore-backed implementation of DeviceStore.
type deviceStore struct {
	ds ds.Batching
	mu sync.RWMutex

	// Index maps for faster lookups
	serialIndex map[string]string // serial -> nodeID
	macIndex    map[string]string // mac -> nodeID
}

// NewDeviceStore creates a new device store with the given datastore.
func NewDeviceStore(datastore ds.Batching) (DeviceStore, error) {
	store := &deviceStore{
		ds:          datastore,
		serialIndex: make(map[string]string),
		macIndex:    make(map[string]string),
	}

	// Rebuild indexes from existing data
	if err := store.rebuildIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to rebuild device indexes: %w", err)
	}

	return store, nil
}

// deviceKeyPrefix is the datastore key prefix for devices.
const deviceKeyPrefix = "/devices/"

// deviceKey returns the datastore key for a device.
func deviceKey(nodeID string) ds.Key {
	return ds.NewKey(deviceKeyPrefix + nodeID)
}

// rebuildIndexes rebuilds the in-memory indexes from the datastore.
func (s *deviceStore) rebuildIndexes(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := query.Query{
		Prefix: deviceKeyPrefix,
	}

	results, err := s.ds.Query(ctx, q)
	if err != nil {
		return err
	}
	defer results.Close()

	for result := range results.Next() {
		if result.Error != nil {
			return result.Error
		}

		var device Device
		if err := json.Unmarshal(result.Value, &device); err != nil {
			continue // Skip invalid entries
		}

		if device.Serial != "" {
			s.serialIndex[device.Serial] = device.NodeID
		}
		if device.MAC != "" {
			s.macIndex[device.MAC] = device.NodeID
		}
	}

	return nil
}

// SaveDevice persists a device registration.
func (s *deviceStore) SaveDevice(ctx context.Context, device *Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("failed to marshal device: %w", err)
	}

	if err := s.ds.Put(ctx, deviceKey(device.NodeID), data); err != nil {
		return fmt.Errorf("failed to save device: %w", err)
	}

	// Update indexes
	if device.Serial != "" {
		s.serialIndex[device.Serial] = device.NodeID
	}
	if device.MAC != "" {
		s.macIndex[device.MAC] = device.NodeID
	}

	return nil
}

// GetDevice retrieves a device by node ID.
func (s *deviceStore) GetDevice(ctx context.Context, nodeID string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.ds.Get(ctx, deviceKey(nodeID))
	if err == ds.ErrNotFound {
		return nil, ErrDeviceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	var device Device
	if err := json.Unmarshal(data, &device); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device: %w", err)
	}

	return &device, nil
}

// GetDeviceBySerial retrieves a device by serial number.
func (s *deviceStore) GetDeviceBySerial(ctx context.Context, serial string) (*Device, error) {
	s.mu.RLock()
	nodeID, ok := s.serialIndex[serial]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrDeviceNotFound
	}

	return s.GetDevice(ctx, nodeID)
}

// GetDeviceByMAC retrieves a device by MAC address.
func (s *deviceStore) GetDeviceByMAC(ctx context.Context, mac string) (*Device, error) {
	s.mu.RLock()
	nodeID, ok := s.macIndex[mac]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrDeviceNotFound
	}

	return s.GetDevice(ctx, nodeID)
}

// ListDevices retrieves all registered devices.
func (s *deviceStore) ListDevices(ctx context.Context) ([]*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := query.Query{
		Prefix: deviceKeyPrefix,
	}

	results, err := s.ds.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to query devices: %w", err)
	}
	defer results.Close()

	var devices []*Device
	for result := range results.Next() {
		if result.Error != nil {
			return nil, result.Error
		}

		var device Device
		if err := json.Unmarshal(result.Value, &device); err != nil {
			continue // Skip invalid entries
		}
		devices = append(devices, &device)
	}

	return devices, nil
}

// ListDevicesBySite retrieves all devices for a given site.
func (s *deviceStore) ListDevicesBySite(ctx context.Context, siteID string) ([]*Device, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}

	var siteDevices []*Device
	for _, d := range devices {
		if d.SiteID == siteID {
			siteDevices = append(siteDevices, d)
		}
	}

	return siteDevices, nil
}

// DeleteDevice removes a device registration.
func (s *deviceStore) DeleteDevice(ctx context.Context, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get device first to clean up indexes
	data, err := s.ds.Get(ctx, deviceKey(nodeID))
	if err == ds.ErrNotFound {
		return nil // Already deleted
	}
	if err != nil {
		return fmt.Errorf("failed to get device for deletion: %w", err)
	}

	var device Device
	if err := json.Unmarshal(data, &device); err == nil {
		// Clean up indexes
		if device.Serial != "" {
			delete(s.serialIndex, device.Serial)
		}
		if device.MAC != "" {
			delete(s.macIndex, device.MAC)
		}
	}

	if err := s.ds.Delete(ctx, deviceKey(nodeID)); err != nil {
		return fmt.Errorf("failed to delete device: %w", err)
	}

	return nil
}

// InMemoryDeviceStore is a simple in-memory implementation for testing.
type InMemoryDeviceStore struct {
	mu      sync.RWMutex
	devices map[string]*Device
	serial  map[string]string
	mac     map[string]string
}

// NewInMemoryDeviceStore creates a new in-memory device store.
func NewInMemoryDeviceStore() *InMemoryDeviceStore {
	return &InMemoryDeviceStore{
		devices: make(map[string]*Device),
		serial:  make(map[string]string),
		mac:     make(map[string]string),
	}
}

func (s *InMemoryDeviceStore) SaveDevice(ctx context.Context, device *Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make a copy to avoid external mutations
	d := *device
	s.devices[d.NodeID] = &d

	if d.Serial != "" {
		s.serial[d.Serial] = d.NodeID
	}
	if d.MAC != "" {
		s.mac[d.MAC] = d.NodeID
	}

	return nil
}

func (s *InMemoryDeviceStore) GetDevice(ctx context.Context, nodeID string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	device, ok := s.devices[nodeID]
	if !ok {
		return nil, ErrDeviceNotFound
	}

	// Return a copy
	d := *device
	return &d, nil
}

func (s *InMemoryDeviceStore) GetDeviceBySerial(ctx context.Context, serial string) (*Device, error) {
	s.mu.RLock()
	nodeID, ok := s.serial[serial]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrDeviceNotFound
	}

	return s.GetDevice(ctx, nodeID)
}

func (s *InMemoryDeviceStore) GetDeviceByMAC(ctx context.Context, mac string) (*Device, error) {
	s.mu.RLock()
	nodeID, ok := s.mac[mac]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrDeviceNotFound
	}

	return s.GetDevice(ctx, nodeID)
}

func (s *InMemoryDeviceStore) ListDevices(ctx context.Context) ([]*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devices := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		device := *d
		devices = append(devices, &device)
	}

	return devices, nil
}

func (s *InMemoryDeviceStore) ListDevicesBySite(ctx context.Context, siteID string) ([]*Device, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}

	var siteDevices []*Device
	for _, d := range devices {
		if d.SiteID == siteID {
			siteDevices = append(siteDevices, d)
		}
	}

	return siteDevices, nil
}

func (s *InMemoryDeviceStore) DeleteDevice(ctx context.Context, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	device, ok := s.devices[nodeID]
	if ok {
		if device.Serial != "" {
			delete(s.serial, device.Serial)
		}
		if device.MAC != "" {
			delete(s.mac, device.MAC)
		}
		delete(s.devices, nodeID)
	}

	return nil
}
