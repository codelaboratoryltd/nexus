// Package audit provides security audit logging for Nexus.
package audit

import (
	"net"
	"time"
)

// EventType represents the type of audit event.
type EventType string

const (
	// API authentication events
	EventAPIAuthAttempt  EventType = "API_AUTH_ATTEMPT"
	EventAPIAuthSuccess  EventType = "API_AUTH_SUCCESS"
	EventAPIAuthFailure  EventType = "API_AUTH_FAILURE"
	EventAPIAccessDenied EventType = "API_ACCESS_DENIED"
	EventAPIRateLimited  EventType = "API_RATE_LIMITED"

	// Device registration events
	EventDeviceRegistrationAttempt EventType = "DEVICE_REGISTRATION_ATTEMPT"
	EventDeviceRegistrationSuccess EventType = "DEVICE_REGISTRATION_SUCCESS"
	EventDeviceRegistrationFailure EventType = "DEVICE_REGISTRATION_FAILURE"
	EventDeviceDeregistration      EventType = "DEVICE_DEREGISTRATION"
	EventDeviceHeartbeat           EventType = "DEVICE_HEARTBEAT"

	// Resource allocation events
	EventPoolCreated        EventType = "POOL_CREATED"
	EventPoolDeleted        EventType = "POOL_DELETED"
	EventPoolModified       EventType = "POOL_MODIFIED"
	EventAllocationCreated  EventType = "ALLOCATION_CREATED"
	EventAllocationDeleted  EventType = "ALLOCATION_DELETED"
	EventAllocationConflict EventType = "ALLOCATION_CONFLICT"
	EventResourceExhausted  EventType = "RESOURCE_EXHAUSTED"

	// Node events
	EventNodeJoined  EventType = "NODE_JOINED"
	EventNodeLeft    EventType = "NODE_LEFT"
	EventNodeExpired EventType = "NODE_EXPIRED"

	// Configuration changes
	EventConfigChange EventType = "CONFIG_CHANGE"
	EventAdminAction  EventType = "ADMIN_ACTION"

	// System events
	EventSystemStart EventType = "SYSTEM_START"
	EventSystemStop  EventType = "SYSTEM_STOP"
	EventSystemError EventType = "SYSTEM_ERROR"

	// Security events
	EventSuspiciousActivity EventType = "SUSPICIOUS_ACTIVITY"
	EventBruteForceDetected EventType = "BRUTE_FORCE_DETECTED"
	EventUnauthorizedAccess EventType = "UNAUTHORIZED_ACCESS"
)

// Severity represents the severity of an event.
type Severity int

const (
	SeverityDebug Severity = iota
	SeverityInfo
	SeverityNotice
	SeverityWarning
	SeverityError
	SeverityCritical
	SeverityAlert
	SeverityEmergency
)

func (s Severity) String() string {
	switch s {
	case SeverityDebug:
		return "DEBUG"
	case SeverityInfo:
		return "INFO"
	case SeverityNotice:
		return "NOTICE"
	case SeverityWarning:
		return "WARNING"
	case SeverityError:
		return "ERROR"
	case SeverityCritical:
		return "CRITICAL"
	case SeverityAlert:
		return "ALERT"
	case SeverityEmergency:
		return "EMERGENCY"
	default:
		return "UNKNOWN"
	}
}

// GetSeverity returns the severity for an event type.
func (e EventType) GetSeverity() Severity {
	switch e {
	case EventAPIAuthFailure, EventAPIAccessDenied, EventAPIRateLimited:
		return SeverityWarning
	case EventDeviceRegistrationFailure:
		return SeverityWarning
	case EventAllocationConflict, EventResourceExhausted:
		return SeverityWarning
	case EventSystemError:
		return SeverityError
	case EventSuspiciousActivity:
		return SeverityWarning
	case EventBruteForceDetected, EventUnauthorizedAccess:
		return SeverityAlert
	case EventAPIAuthSuccess, EventDeviceRegistrationSuccess:
		return SeverityInfo
	case EventPoolCreated, EventPoolDeleted, EventAllocationCreated, EventAllocationDeleted:
		return SeverityInfo
	case EventNodeJoined, EventNodeLeft, EventNodeExpired:
		return SeverityNotice
	case EventConfigChange, EventAdminAction:
		return SeverityNotice
	default:
		return SeverityInfo
	}
}

// Category returns the category for an event type.
func (e EventType) Category() string {
	switch e {
	case EventAPIAuthAttempt, EventAPIAuthSuccess, EventAPIAuthFailure, EventAPIAccessDenied, EventAPIRateLimited:
		return "api"
	case EventDeviceRegistrationAttempt, EventDeviceRegistrationSuccess, EventDeviceRegistrationFailure, EventDeviceDeregistration, EventDeviceHeartbeat:
		return "device"
	case EventPoolCreated, EventPoolDeleted, EventPoolModified, EventAllocationCreated, EventAllocationDeleted, EventAllocationConflict, EventResourceExhausted:
		return "resource"
	case EventNodeJoined, EventNodeLeft, EventNodeExpired:
		return "node"
	case EventConfigChange, EventAdminAction:
		return "admin"
	case EventSystemStart, EventSystemStop, EventSystemError:
		return "system"
	case EventSuspiciousActivity, EventBruteForceDetected, EventUnauthorizedAccess:
		return "security"
	default:
		return "other"
	}
}

// Event represents a single audit event.
type Event struct {
	// Core fields
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	ServerID  string    `json:"server_id"` // Nexus server ID

	// Actor information
	ActorID   string `json:"actor_id,omitempty"`   // User, device, or service ID
	ActorType string `json:"actor_type,omitempty"` // "user", "device", "service", "system"

	// Request context
	RequestID   string `json:"request_id,omitempty"`   // Unique request identifier for tracing
	SourceIP    net.IP `json:"source_ip,omitempty"`    // Client IP address
	UserAgent   string `json:"user_agent,omitempty"`   // HTTP user agent
	APIEndpoint string `json:"api_endpoint,omitempty"` // API endpoint accessed
	HTTPMethod  string `json:"http_method,omitempty"`  // HTTP method
	HTTPStatus  int    `json:"http_status,omitempty"`  // Response status code

	// Resource context
	ResourceType string `json:"resource_type,omitempty"` // "pool", "allocation", "node"
	ResourceID   string `json:"resource_id,omitempty"`   // ID of the affected resource
	PoolID       string `json:"pool_id,omitempty"`       // Pool ID for allocation events
	SubscriberID string `json:"subscriber_id,omitempty"` // Subscriber ID
	DeviceID     string `json:"device_id,omitempty"`     // Device ID for device events
	NodeID       string `json:"node_id,omitempty"`       // Node ID for node events

	// Result information
	Success      bool   `json:"success"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	// Security context
	ThreatType   string    `json:"threat_type,omitempty"`   // Type of threat detected
	ThreatScore  int       `json:"threat_score,omitempty"`  // Risk score 0-100
	FailureCount int       `json:"failure_count,omitempty"` // Number of failures (for brute force)
	BlockedUntil time.Time `json:"blocked_until,omitempty"` // Time until which actor is blocked

	// Change details (for config changes)
	OldValue string `json:"old_value,omitempty"` // Previous value
	NewValue string `json:"new_value,omitempty"` // New value

	// Additional metadata
	Metadata map[string]string `json:"metadata,omitempty"`

	// Retention
	RetentionDays int       `json:"retention_days,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}
