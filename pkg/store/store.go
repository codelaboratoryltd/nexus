package store

import (
	"context"
	"net"
	"time"
)

// Subscriber represents a subscriber record
type Subscriber struct {
	ID           string           `json:"id"`
	MAC          net.HardwareAddr `json:"mac"`
	IP           net.IP           `json:"ip"`
	OLTID        string           `json:"olt_id"`
	VLAN         uint16           `json:"vlan"`
	CircuitID    string           `json:"circuit_id"`
	Username     string           `json:"username"`
	SessionStart time.Time        `json:"session_start"`
	LastSeen     time.Time        `json:"last_seen"`
	State        string           `json:"state"` // active, suspended, terminated
}

// OLT represents an OLT-BNG node
type OLT struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Region        string    `json:"region"`
	Capacity      int       `json:"capacity"`
	ActiveSubs    int       `json:"active_subs"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	State         string    `json:"state"` // bootstrapping, active, draining, offline
}

// IPPool represents an IP address pool
type IPPool struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	CIDR       string        `json:"cidr"`
	Gateway    net.IP        `json:"gateway"`
	DNSServers []net.IP      `json:"dns_servers"`
	LeaseTime  time.Duration `json:"lease_time"`
	Allocated  int           `json:"allocated"`
	Available  int           `json:"available"`
}

// Store defines the interface for state storage
type Store interface {
	// Subscriber operations
	GetSubscriber(ctx context.Context, id string) (*Subscriber, error)
	GetSubscriberByMAC(ctx context.Context, mac net.HardwareAddr) (*Subscriber, error)
	ListSubscribers(ctx context.Context, oltID string) ([]*Subscriber, error)
	CreateSubscriber(ctx context.Context, sub *Subscriber) error
	UpdateSubscriber(ctx context.Context, sub *Subscriber) error
	DeleteSubscriber(ctx context.Context, id string) error

	// OLT operations
	GetOLT(ctx context.Context, id string) (*OLT, error)
	ListOLTs(ctx context.Context) ([]*OLT, error)
	RegisterOLT(ctx context.Context, olt *OLT) error
	UpdateOLT(ctx context.Context, olt *OLT) error
	DeregisterOLT(ctx context.Context, id string) error

	// IP Pool operations
	GetPool(ctx context.Context, id string) (*IPPool, error)
	ListPools(ctx context.Context) ([]*IPPool, error)

	// Health
	Ping(ctx context.Context) error
	Close() error
}
