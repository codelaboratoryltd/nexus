// Package rendezvousdb provides database backends for the libp2p rendezvous service.
// It implements the DB interface from github.com/waku-org/go-libp2p-rendezvous/db
// without requiring CGO (unlike the default SQLite implementation).
package rendezvousdb

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	dbi "github.com/waku-org/go-libp2p-rendezvous/db"
)

// registration is an internal record with expiry tracking.
type registration struct {
	record    dbi.RegistrationRecord
	expiresAt time.Time
	id        uint64
}

// MemoryDB is an in-memory implementation of the rendezvous DB interface.
// It's suitable for development and testing where persistence isn't required.
type MemoryDB struct {
	mu            sync.RWMutex
	registrations map[string]map[peer.ID]*registration // ns -> peer.ID -> registration
	nextID        uint64
	closed        bool
}

// NewMemoryDB creates a new in-memory rendezvous database.
func NewMemoryDB() *MemoryDB {
	db := &MemoryDB{
		registrations: make(map[string]map[peer.ID]*registration),
		nextID:        1,
	}
	// Start background cleanup goroutine
	go db.cleanupLoop()
	return db
}

// Close closes the database.
func (db *MemoryDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.closed = true
	return nil
}

// Register registers a peer for a namespace with a TTL.
func (db *MemoryDB) Register(p peer.ID, ns string, signedPeerRecord []byte, ttl int) (uint64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return 0, ErrClosed
	}

	if db.registrations[ns] == nil {
		db.registrations[ns] = make(map[peer.ID]*registration)
	}

	id := db.nextID
	db.nextID++

	db.registrations[ns][p] = &registration{
		record: dbi.RegistrationRecord{
			Id:               p,
			SignedPeerRecord: signedPeerRecord,
			Ns:               ns,
			Ttl:              ttl,
		},
		expiresAt: time.Now().Add(time.Duration(ttl) * time.Second),
		id:        id,
	}

	return id, nil
}

// Unregister removes a peer's registration for a namespace.
func (db *MemoryDB) Unregister(p peer.ID, ns string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrClosed
	}

	if db.registrations[ns] != nil {
		delete(db.registrations[ns], p)
	}
	return nil
}

// CountRegistrations returns the number of registrations for a peer.
func (db *MemoryDB) CountRegistrations(p peer.ID) (int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return 0, ErrClosed
	}

	count := 0
	now := time.Now()
	for _, nsRegs := range db.registrations {
		if reg, ok := nsRegs[p]; ok {
			if reg.expiresAt.After(now) {
				count++
			}
		}
	}
	return count, nil
}

// Discover finds peer registrations for a namespace.
// Returns registrations, a cookie for pagination, and any error.
func (db *MemoryDB) Discover(ns string, cookie []byte, limit int) ([]dbi.RegistrationRecord, []byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, nil, ErrClosed
	}

	nsRegs := db.registrations[ns]
	if nsRegs == nil {
		return nil, nil, nil
	}

	// Parse cookie to get starting position
	var startID uint64
	if len(cookie) >= 8 {
		startID = binary.BigEndian.Uint64(cookie)
	}

	now := time.Now()
	var results []dbi.RegistrationRecord
	var maxID uint64

	for _, reg := range nsRegs {
		// Skip expired registrations
		if reg.expiresAt.Before(now) {
			continue
		}
		// Skip if before cookie position
		if reg.id <= startID {
			continue
		}

		results = append(results, reg.record)
		if reg.id > maxID {
			maxID = reg.id
		}

		if limit > 0 && len(results) >= limit {
			break
		}
	}

	// Generate new cookie if there are results
	var newCookie []byte
	if len(results) > 0 {
		newCookie = make([]byte, 8)
		binary.BigEndian.PutUint64(newCookie, maxID)
	}

	return results, newCookie, nil
}

// ValidCookie checks if a cookie is valid for the given namespace.
func (db *MemoryDB) ValidCookie(ns string, cookie []byte) bool {
	if len(cookie) == 0 {
		return true
	}
	// Simple validation: cookie should be 8 bytes (uint64)
	return len(cookie) == 8
}

// cleanupLoop periodically removes expired registrations.
func (db *MemoryDB) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		db.mu.Lock()
		if db.closed {
			db.mu.Unlock()
			return
		}

		now := time.Now()
		for ns, nsRegs := range db.registrations {
			for p, reg := range nsRegs {
				if reg.expiresAt.Before(now) {
					delete(nsRegs, p)
				}
			}
			if len(nsRegs) == 0 {
				delete(db.registrations, ns)
			}
		}
		db.mu.Unlock()
	}
}
