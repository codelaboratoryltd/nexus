package rendezvousdb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/libp2p/go-libp2p/core/peer"
	dbi "github.com/waku-org/go-libp2p-rendezvous/db"
)

// Key prefixes for BadgerDB storage
const (
	prefixRegistration = "rvz:reg:"   // rvz:reg:<ns>:<peer_id> -> registration JSON
	prefixCounter      = "rvz:cnt:"   // rvz:cnt:<ns> -> uint64 counter
	prefixGlobal       = "rvz:global" // rvz:global:id -> global ID counter
)

// badgerRegistration is the stored format for registrations.
type badgerRegistration struct {
	SignedPeerRecord []byte    `json:"signed_peer_record"`
	Ns               string    `json:"ns"`
	Ttl              int       `json:"ttl"`
	ExpiresAt        time.Time `json:"expires_at"`
	ID               uint64    `json:"id"`
}

// BadgerDB is a BadgerDB implementation of the rendezvous DB interface.
// It provides persistent storage suitable for production use.
type BadgerDB struct {
	db *badger.DB
}

// NewBadgerDB creates a new BadgerDB-backed rendezvous database.
func NewBadgerDB(path string) (*BadgerDB, error) {
	opts := badger.DefaultOptions(path)
	opts.Logger = nil // Disable badger logging

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	bdb := &BadgerDB{db: db}

	// Start background GC and cleanup
	go bdb.gcLoop()
	go bdb.cleanupLoop()

	return bdb, nil
}

// Close closes the database.
func (db *BadgerDB) Close() error {
	return db.db.Close()
}

// Register registers a peer for a namespace with a TTL.
func (db *BadgerDB) Register(p peer.ID, ns string, signedPeerRecord []byte, ttl int) (uint64, error) {
	var id uint64

	err := db.db.Update(func(txn *badger.Txn) error {
		// Get and increment global ID
		var err error
		id, err = db.getAndIncrementID(txn)
		if err != nil {
			return err
		}

		// Create registration record
		reg := badgerRegistration{
			SignedPeerRecord: signedPeerRecord,
			Ns:               ns,
			Ttl:              ttl,
			ExpiresAt:        time.Now().Add(time.Duration(ttl) * time.Second),
			ID:               id,
		}

		data, err := json.Marshal(reg)
		if err != nil {
			return fmt.Errorf("failed to marshal registration: %w", err)
		}

		// Store with TTL
		key := []byte(fmt.Sprintf("%s%s:%s", prefixRegistration, ns, p.String()))
		entry := badger.NewEntry(key, data).WithTTL(time.Duration(ttl) * time.Second)
		return txn.SetEntry(entry)
	})

	return id, err
}

// Unregister removes a peer's registration for a namespace.
func (db *BadgerDB) Unregister(p peer.ID, ns string) error {
	return db.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf("%s%s:%s", prefixRegistration, ns, p.String()))
		return txn.Delete(key)
	})
}

// CountRegistrations returns the number of registrations for a peer.
func (db *BadgerDB) CountRegistrations(p peer.ID) (int, error) {
	count := 0
	peerStr := p.String()

	err := db.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixRegistration)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := string(it.Item().Key())
			// Check if this key ends with our peer ID
			if len(key) > len(peerStr) && key[len(key)-len(peerStr):] == peerStr {
				count++
			}
		}
		return nil
	})

	return count, err
}

// Discover finds peer registrations for a namespace.
func (db *BadgerDB) Discover(ns string, cookie []byte, limit int) ([]dbi.RegistrationRecord, []byte, error) {
	var results []dbi.RegistrationRecord
	var maxID uint64

	// Parse cookie to get starting position
	var startID uint64
	if len(cookie) >= 8 {
		startID = binary.BigEndian.Uint64(cookie)
	}

	err := db.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(fmt.Sprintf("%s%s:", prefixRegistration, ns))
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				var reg badgerRegistration
				if err := json.Unmarshal(val, &reg); err != nil {
					return err
				}

				// Skip if expired (shouldn't happen due to TTL, but be safe)
				if reg.ExpiresAt.Before(time.Now()) {
					return nil
				}

				// Skip if before cookie position
				if reg.ID <= startID {
					return nil
				}

				// Extract peer ID from key
				key := string(item.Key())
				peerIDStr := key[len(prefix):]
				peerID, err := peer.Decode(peerIDStr)
				if err != nil {
					return nil // Skip invalid entries
				}

				results = append(results, dbi.RegistrationRecord{
					Id:               peerID,
					SignedPeerRecord: reg.SignedPeerRecord,
					Ns:               reg.Ns,
					Ttl:              reg.Ttl,
				})

				if reg.ID > maxID {
					maxID = reg.ID
				}

				return nil
			})
			if err != nil {
				return err
			}

			if limit > 0 && len(results) >= limit {
				break
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
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
func (db *BadgerDB) ValidCookie(ns string, cookie []byte) bool {
	if len(cookie) == 0 {
		return true
	}
	return len(cookie) == 8
}

// getAndIncrementID atomically gets and increments the global ID counter.
func (db *BadgerDB) getAndIncrementID(txn *badger.Txn) (uint64, error) {
	key := []byte(prefixGlobal + ":id")

	var id uint64 = 1
	item, err := txn.Get(key)
	if err == nil {
		err = item.Value(func(val []byte) error {
			if len(val) == 8 {
				id = binary.BigEndian.Uint64(val) + 1
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	} else if err != badger.ErrKeyNotFound {
		return 0, err
	}

	// Store incremented value
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, id)
	if err := txn.Set(key, val); err != nil {
		return 0, err
	}

	return id, nil
}

// gcLoop runs periodic garbage collection on the BadgerDB.
func (db *BadgerDB) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		// Run GC until it returns nil (no more cleanup needed)
		for {
			err := db.db.RunValueLogGC(0.5)
			if err != nil {
				break
			}
		}
	}
}

// cleanupLoop periodically cleans up expired entries.
// Note: BadgerDB TTL should handle this automatically, but this is a safety net.
func (db *BadgerDB) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		_ = db.db.Update(func(txn *badger.Txn) error {
			opts := badger.DefaultIteratorOptions
			it := txn.NewIterator(opts)
			defer it.Close()

			var keysToDelete [][]byte
			now := time.Now()

			prefix := []byte(prefixRegistration)
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				_ = item.Value(func(val []byte) error {
					var reg badgerRegistration
					if err := json.Unmarshal(val, &reg); err != nil {
						return nil
					}
					if reg.ExpiresAt.Before(now) {
						keysToDelete = append(keysToDelete, item.KeyCopy(nil))
					}
					return nil
				})
			}

			for _, key := range keysToDelete {
				_ = txn.Delete(key)
			}
			return nil
		})
	}
}
