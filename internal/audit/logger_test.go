package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEventType_GetSeverity(t *testing.T) {
	tests := []struct {
		eventType EventType
		severity  Severity
	}{
		{EventAPIAuthSuccess, SeverityInfo},
		{EventAPIAuthFailure, SeverityWarning},
		{EventAPIAccessDenied, SeverityWarning},
		{EventDeviceRegistrationSuccess, SeverityInfo},
		{EventDeviceRegistrationFailure, SeverityWarning},
		{EventBruteForceDetected, SeverityAlert},
		{EventSystemError, SeverityError},
		{EventPoolCreated, SeverityInfo},
	}

	for _, tt := range tests {
		if got := tt.eventType.GetSeverity(); got != tt.severity {
			t.Errorf("EventType(%s).GetSeverity() = %v, want %v", tt.eventType, got, tt.severity)
		}
	}
}

func TestEventType_Category(t *testing.T) {
	tests := []struct {
		eventType EventType
		category  string
	}{
		{EventAPIAuthSuccess, "api"},
		{EventAPIAuthFailure, "api"},
		{EventDeviceRegistrationSuccess, "device"},
		{EventPoolCreated, "resource"},
		{EventAllocationCreated, "resource"},
		{EventNodeJoined, "node"},
		{EventConfigChange, "admin"},
		{EventSystemStart, "system"},
		{EventBruteForceDetected, "security"},
	}

	for _, tt := range tests {
		if got := tt.eventType.Category(); got != tt.category {
			t.Errorf("EventType(%s).Category() = %s, want %s", tt.eventType, got, tt.category)
		}
	}
}

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{SeverityDebug, "DEBUG"},
		{SeverityInfo, "INFO"},
		{SeverityWarning, "WARNING"},
		{SeverityError, "ERROR"},
		{SeverityAlert, "ALERT"},
	}

	for _, tt := range tests {
		if got := tt.severity.String(); got != tt.expected {
			t.Errorf("Severity(%d).String() = %s, want %s", tt.severity, got, tt.expected)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.BufferSize == 0 {
		t.Error("BufferSize should not be 0")
	}

	if config.FlushInterval == 0 {
		t.Error("FlushInterval should not be 0")
	}

	if config.DefaultRetentionDays == 0 {
		t.Error("DefaultRetentionDays should not be 0")
	}

	if !config.JSONFormat {
		t.Error("JSONFormat should be true by default")
	}

	if len(config.RetentionByCategory) == 0 {
		t.Error("RetentionByCategory should not be empty")
	}
}

func TestLogger_StartStop(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)

	if err := logger.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := logger.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Should have system start and stop events
	output := buf.String()
	if !strings.Contains(output, "SYSTEM_START") {
		t.Error("Output should contain SYSTEM_START")
	}
	if !strings.Contains(output, "SYSTEM_STOP") {
		t.Error("Output should contain SYSTEM_STOP")
	}
}

func TestLogger_LogEvent_JSON(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true
	config.JSONFormat = true

	logger := NewLogger(config)

	event := &Event{
		Type:        EventAPIAuthSuccess,
		ActorID:     "user-123",
		ActorType:   "user",
		APIEndpoint: "/api/v1/pools",
		HTTPMethod:  "GET",
		HTTPStatus:  200,
		Success:     true,
	}

	logger.LogEvent(event)

	// Parse the JSON output
	var parsed Event
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if parsed.Type != EventAPIAuthSuccess {
		t.Errorf("Type = %s, want %s", parsed.Type, EventAPIAuthSuccess)
	}
	if parsed.ActorID != "user-123" {
		t.Errorf("ActorID = %s, want user-123", parsed.ActorID)
	}
	if parsed.ServerID != "test-server" {
		t.Errorf("ServerID = %s, want test-server", parsed.ServerID)
	}
	if parsed.ID == "" {
		t.Error("Event should have an ID")
	}
	if parsed.Timestamp.IsZero() {
		t.Error("Event should have a timestamp")
	}
}

func TestLogger_LogEvent_Text(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true
	config.JSONFormat = false

	logger := NewLogger(config)

	event := &Event{
		Type:         EventAPIAuthFailure,
		ActorID:      "user-456",
		ActorType:    "user",
		APIEndpoint:  "/api/v1/allocations",
		HTTPMethod:   "POST",
		HTTPStatus:   403,
		Success:      false,
		ErrorMessage: "access denied",
	}

	logger.LogEvent(event)

	output := buf.String()
	expectedParts := []string{
		"server=test-server",
		"type=API_AUTH_FAILURE",
		"actor=user-456",
		"endpoint=/api/v1/allocations",
		"method=POST",
		"status=403",
		"success=false",
		"error=\"access denied\"",
	}

	for _, part := range expectedParts {
		if !strings.Contains(output, part) {
			t.Errorf("Output should contain %q, got: %s", part, output)
		}
	}
}

func TestLogger_LogAPIAccess(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)
	ctx := context.Background()

	// Log successful API access
	event := &Event{
		ActorID:     "user-789",
		ActorType:   "user",
		APIEndpoint: "/api/v1/pools",
		HTTPMethod:  "GET",
		HTTPStatus:  200,
	}
	logger.LogAPIAccess(ctx, event, true)

	output := buf.String()
	if !strings.Contains(output, "API_AUTH_SUCCESS") {
		t.Errorf("Output should contain API_AUTH_SUCCESS, got: %s", output)
	}
}

func TestLogger_LogDeviceRegistration(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)
	ctx := context.Background()

	// Log successful registration
	logger.LogDeviceRegistration(ctx, "device-001", true, "")

	output := buf.String()
	if !strings.Contains(output, "DEVICE_REGISTRATION_SUCCESS") {
		t.Errorf("Output should contain DEVICE_REGISTRATION_SUCCESS, got: %s", output)
	}
	if !strings.Contains(output, "device-001") {
		t.Errorf("Output should contain device ID, got: %s", output)
	}
}

func TestLogger_LogResourceAllocation(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)
	ctx := context.Background()

	// Log allocation
	logger.LogResourceAllocation(ctx, "pool-1", "sub-123", "10.0.0.1", true, "")

	output := buf.String()
	if !strings.Contains(output, "ALLOCATION_CREATED") {
		t.Errorf("Output should contain ALLOCATION_CREATED, got: %s", output)
	}
}

func TestLogger_LogBruteForce(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.ServerID = "test-server"
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)
	ctx := context.Background()

	blockedUntil := time.Now().Add(15 * time.Minute)
	logger.LogBruteForce(ctx, "attacker", "192.168.1.100", 10, blockedUntil)

	output := buf.String()
	if !strings.Contains(output, "BRUTE_FORCE_DETECTED") {
		t.Errorf("Output should contain BRUTE_FORCE_DETECTED, got: %s", output)
	}
}

func TestLogger_Stats(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)

	// Log some events
	for i := 0; i < 5; i++ {
		logger.LogEvent(&Event{
			Type: EventAPIAuthSuccess,
		})
	}

	stats := logger.Stats()
	if stats.EventsLogged != 5 {
		t.Errorf("EventsLogged = %d, want 5", stats.EventsLogged)
	}
}

func TestLogger_Retention(t *testing.T) {
	var buf bytes.Buffer
	config := DefaultConfig()
	config.Writer = &buf
	config.SyncWrites = true

	logger := NewLogger(config)

	event := &Event{
		Type: EventBruteForceDetected, // Security category
	}
	logger.LogEvent(event)

	// Parse the output to check retention
	var parsed Event
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Security events should have 730 days retention
	expectedRetention := config.RetentionByCategory["security"]
	if parsed.RetentionDays != expectedRetention {
		t.Errorf("RetentionDays = %d, want %d", parsed.RetentionDays, expectedRetention)
	}
}
