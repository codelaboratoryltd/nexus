package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// DeviceStatus represents the approval status of a device in the whitelist.
type DeviceStatus string

const (
	DeviceStatusPending  DeviceStatus = "pending"
	DeviceStatusApproved DeviceStatus = "approved"
	DeviceStatusRevoked  DeviceStatus = "revoked"
)

// WhitelistEntry represents a device in the whitelist.
type WhitelistEntry struct {
	Serial     string            `json:"serial"`
	Status     DeviceStatus      `json:"status"`
	AddedAt    time.Time         `json:"added_at"`
	ApprovedAt *time.Time        `json:"approved_at,omitempty"`
	RevokedAt  *time.Time        `json:"revoked_at,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// DeviceWhitelist manages approved device serial numbers with file persistence.
type DeviceWhitelist struct {
	mu       sync.RWMutex
	devices  map[string]*WhitelistEntry // keyed by serial number
	filePath string                     // path for JSON persistence, empty = no persistence
}

// NewDeviceWhitelist creates a new device whitelist.
// If filePath is non-empty, the whitelist is loaded from and persisted to that file.
func NewDeviceWhitelist(filePath string) (*DeviceWhitelist, error) {
	w := &DeviceWhitelist{
		devices:  make(map[string]*WhitelistEntry),
		filePath: filePath,
	}

	if filePath != "" {
		if err := w.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load whitelist: %w", err)
		}
	}

	return w, nil
}

// Add adds a device serial to the whitelist as pending.
// If the serial already exists, it returns an error.
func (w *DeviceWhitelist) Add(serial string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.devices[serial]; exists {
		return fmt.Errorf("device %s already exists in whitelist", serial)
	}

	w.devices[serial] = &WhitelistEntry{
		Serial:  serial,
		Status:  DeviceStatusPending,
		AddedAt: time.Now(),
	}

	return w.saveLocked()
}

// Approve transitions a pending device to approved status.
func (w *DeviceWhitelist) Approve(serial string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry, exists := w.devices[serial]
	if !exists {
		return fmt.Errorf("device %s not found in whitelist", serial)
	}

	if entry.Status == DeviceStatusApproved {
		return nil // already approved
	}

	if entry.Status == DeviceStatusRevoked {
		return fmt.Errorf("device %s is revoked, re-add to approve", serial)
	}

	now := time.Now()
	entry.Status = DeviceStatusApproved
	entry.ApprovedAt = &now

	return w.saveLocked()
}

// Revoke transitions a device to revoked status.
func (w *DeviceWhitelist) Revoke(serial string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry, exists := w.devices[serial]
	if !exists {
		return fmt.Errorf("device %s not found in whitelist", serial)
	}

	now := time.Now()
	entry.Status = DeviceStatusRevoked
	entry.RevokedAt = &now

	return w.saveLocked()
}

// Remove removes a device from the whitelist entirely.
func (w *DeviceWhitelist) Remove(serial string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.devices[serial]; !exists {
		return fmt.Errorf("device %s not found in whitelist", serial)
	}

	delete(w.devices, serial)
	return w.saveLocked()
}

// Check returns whether a device serial is approved.
func (w *DeviceWhitelist) Check(serial string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	entry, exists := w.devices[serial]
	if !exists {
		return false
	}

	return entry.Status == DeviceStatusApproved
}

// Get returns the whitelist entry for a serial, or nil if not found.
func (w *DeviceWhitelist) Get(serial string) *WhitelistEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()

	entry, exists := w.devices[serial]
	if !exists {
		return nil
	}

	// Return a copy
	e := *entry
	return &e
}

// ListPending returns all devices with pending status.
func (w *DeviceWhitelist) ListPending() []*WhitelistEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var pending []*WhitelistEntry
	for _, entry := range w.devices {
		if entry.Status == DeviceStatusPending {
			e := *entry
			pending = append(pending, &e)
		}
	}

	return pending
}

// ListAll returns all devices in the whitelist.
func (w *DeviceWhitelist) ListAll() []*WhitelistEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()

	all := make([]*WhitelistEntry, 0, len(w.devices))
	for _, entry := range w.devices {
		e := *entry
		all = append(all, &e)
	}

	return all
}

// load reads the whitelist from the JSON file.
func (w *DeviceWhitelist) load() error {
	data, err := os.ReadFile(w.filePath)
	if err != nil {
		return err
	}

	var entries []*WhitelistEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to unmarshal whitelist: %w", err)
	}

	for _, entry := range entries {
		w.devices[entry.Serial] = entry
	}

	return nil
}

// saveLocked writes the whitelist to the JSON file. Must be called with mu held.
func (w *DeviceWhitelist) saveLocked() error {
	if w.filePath == "" {
		return nil
	}

	entries := make([]*WhitelistEntry, 0, len(w.devices))
	for _, entry := range w.devices {
		entries = append(entries, entry)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal whitelist: %w", err)
	}

	if err := os.WriteFile(w.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write whitelist: %w", err)
	}

	return nil
}
