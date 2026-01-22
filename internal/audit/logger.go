package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Logger is the audit logging system for Nexus.
type Logger struct {
	config Config
	writer io.Writer

	mu sync.RWMutex

	// Event buffer for batch writing
	eventChan chan *Event
	buffer    []*Event

	// Statistics
	stats LoggerStats

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Config holds audit logger configuration.
type Config struct {
	// ServerID identifies this Nexus server.
	ServerID string

	// Writer is where audit logs are written (defaults to stdout).
	Writer io.Writer

	// BufferSize is the event buffer size for async processing.
	BufferSize int

	// FlushInterval is how often to flush buffered events.
	FlushInterval time.Duration

	// DefaultRetentionDays is the default retention period.
	DefaultRetentionDays int

	// RetentionByCategory allows different retention per event category.
	RetentionByCategory map[string]int

	// MinSeverity is the minimum severity to log.
	MinSeverity Severity

	// SyncWrites forces synchronous writes (slower but safer).
	SyncWrites bool

	// JSONFormat enables JSON output format.
	JSONFormat bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Writer:               os.Stdout,
		BufferSize:           10000,
		FlushInterval:        5 * time.Second,
		DefaultRetentionDays: 90,
		RetentionByCategory: map[string]int{
			"api":      365, // 1 year for API access logs
			"device":   365, // 1 year for device events
			"resource": 365, // 1 year for resource allocation
			"node":     90,  // 90 days for node events
			"admin":    730, // 2 years for admin actions
			"system":   30,  // 30 days for system events
			"security": 730, // 2 years for security events
		},
		MinSeverity: SeverityDebug,
		JSONFormat:  true, // Default to structured JSON logging
	}
}

// LoggerStats holds audit logger statistics.
type LoggerStats struct {
	EventsLogged  int64
	EventsDropped int64
	BufferSize    int
	WriteErrors   int64
}

// NewLogger creates a new audit logger.
func NewLogger(config Config) *Logger {
	if config.Writer == nil {
		config.Writer = os.Stdout
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Logger{
		config:    config,
		writer:    config.Writer,
		eventChan: make(chan *Event, config.BufferSize),
		buffer:    make([]*Event, 0, 1000),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start begins the audit logger.
func (l *Logger) Start() error {
	// Log system start
	l.LogEvent(&Event{
		Type:     EventSystemStart,
		ServerID: l.config.ServerID,
		Success:  true,
		Metadata: map[string]string{
			"version": "1.0.0",
		},
	})

	// Start async processor
	if !l.config.SyncWrites {
		l.wg.Add(1)
		go l.processEvents()
	}

	// Start flush loop
	l.wg.Add(1)
	go l.flushLoop()

	return nil
}

// Stop shuts down the audit logger.
func (l *Logger) Stop() error {
	// Log system stop
	l.LogEvent(&Event{
		Type:     EventSystemStop,
		ServerID: l.config.ServerID,
		Success:  true,
	})

	l.cancel()
	close(l.eventChan)
	l.wg.Wait()

	// Final flush
	l.flush()

	return nil
}

// LogEvent logs a single audit event.
func (l *Logger) LogEvent(event *Event) {
	l.prepareEvent(event)

	if !l.shouldLog(event) {
		return
	}

	if l.config.SyncWrites {
		l.writeEvent(event)
	} else {
		select {
		case l.eventChan <- event:
		default:
			l.mu.Lock()
			l.stats.EventsDropped++
			l.mu.Unlock()
		}
	}
}

// prepareEvent fills in default fields.
func (l *Logger) prepareEvent(event *Event) {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ServerID == "" {
		event.ServerID = l.config.ServerID
	}

	// Set retention
	category := event.Type.Category()
	if days, ok := l.config.RetentionByCategory[category]; ok {
		event.RetentionDays = days
	} else {
		event.RetentionDays = l.config.DefaultRetentionDays
	}
	event.ExpiresAt = event.Timestamp.AddDate(0, 0, event.RetentionDays)
}

// shouldLog checks if an event should be logged based on config.
func (l *Logger) shouldLog(event *Event) bool {
	return event.Type.GetSeverity() >= l.config.MinSeverity
}

// writeEvent writes a single event to the output.
func (l *Logger) writeEvent(event *Event) {
	var output []byte
	var err error

	if l.config.JSONFormat {
		output, err = json.Marshal(event)
		if err != nil {
			l.mu.Lock()
			l.stats.WriteErrors++
			l.mu.Unlock()
			return
		}
		output = append(output, '\n')
	} else {
		output = []byte(l.formatText(event) + "\n")
	}

	l.mu.Lock()
	_, err = l.writer.Write(output)
	if err != nil {
		l.stats.WriteErrors++
	} else {
		l.stats.EventsLogged++
	}
	l.mu.Unlock()
}

// formatText formats an event as human-readable text.
func (l *Logger) formatText(event *Event) string {
	msg := fmt.Sprintf("[%s] %s server=%s type=%s",
		event.Timestamp.Format(time.RFC3339),
		event.Type.GetSeverity().String(),
		event.ServerID,
		event.Type,
	)

	if event.ActorID != "" {
		msg += fmt.Sprintf(" actor=%s", event.ActorID)
	}
	if event.ActorType != "" {
		msg += fmt.Sprintf(" actor_type=%s", event.ActorType)
	}
	if event.SourceIP != nil {
		msg += fmt.Sprintf(" source_ip=%s", event.SourceIP)
	}
	if event.APIEndpoint != "" {
		msg += fmt.Sprintf(" endpoint=%s", event.APIEndpoint)
	}
	if event.HTTPMethod != "" {
		msg += fmt.Sprintf(" method=%s", event.HTTPMethod)
	}
	if event.HTTPStatus != 0 {
		msg += fmt.Sprintf(" status=%d", event.HTTPStatus)
	}
	if event.ResourceType != "" {
		msg += fmt.Sprintf(" resource_type=%s", event.ResourceType)
	}
	if event.ResourceID != "" {
		msg += fmt.Sprintf(" resource_id=%s", event.ResourceID)
	}
	if event.PoolID != "" {
		msg += fmt.Sprintf(" pool=%s", event.PoolID)
	}
	if event.SubscriberID != "" {
		msg += fmt.Sprintf(" subscriber=%s", event.SubscriberID)
	}
	if event.DeviceID != "" {
		msg += fmt.Sprintf(" device=%s", event.DeviceID)
	}
	if event.NodeID != "" {
		msg += fmt.Sprintf(" node=%s", event.NodeID)
	}
	if !event.Success {
		msg += " success=false"
	}
	if event.ErrorCode != "" {
		msg += fmt.Sprintf(" error_code=%s", event.ErrorCode)
	}
	if event.ErrorMessage != "" {
		msg += fmt.Sprintf(" error=%q", event.ErrorMessage)
	}
	if event.ThreatType != "" {
		msg += fmt.Sprintf(" threat=%s", event.ThreatType)
	}
	if event.ThreatScore > 0 {
		msg += fmt.Sprintf(" threat_score=%d", event.ThreatScore)
	}

	return msg
}

// processEvents processes events from the channel.
func (l *Logger) processEvents() {
	defer l.wg.Done()

	for event := range l.eventChan {
		l.mu.Lock()
		l.buffer = append(l.buffer, event)
		bufLen := len(l.buffer)
		l.mu.Unlock()

		if bufLen >= cap(l.buffer)*80/100 {
			l.flush()
		}
	}
}

// flushLoop periodically flushes the buffer.
func (l *Logger) flushLoop() {
	defer l.wg.Done()

	ticker := time.NewTicker(l.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			l.flush()
		}
	}
}

// flush writes buffered events.
func (l *Logger) flush() {
	l.mu.Lock()
	if len(l.buffer) == 0 {
		l.mu.Unlock()
		return
	}

	events := l.buffer
	l.buffer = make([]*Event, 0, 1000)
	l.mu.Unlock()

	for _, event := range events {
		l.writeEvent(event)
	}
}

// Stats returns logger statistics.
func (l *Logger) Stats() LoggerStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stats := l.stats
	stats.BufferSize = len(l.buffer)
	return stats
}

// Helper methods for common event types

// LogAPIAccess logs an API access event.
func (l *Logger) LogAPIAccess(ctx context.Context, event *Event, success bool) {
	event.Success = success
	if success {
		event.Type = EventAPIAuthSuccess
	} else {
		event.Type = EventAPIAuthFailure
	}
	l.LogEvent(event)
}

// LogDeviceRegistration logs a device registration event.
func (l *Logger) LogDeviceRegistration(ctx context.Context, deviceID string, success bool, errMsg string) {
	event := &Event{
		DeviceID:  deviceID,
		ActorID:   deviceID,
		ActorType: "device",
		Success:   success,
	}
	if success {
		event.Type = EventDeviceRegistrationSuccess
	} else {
		event.Type = EventDeviceRegistrationFailure
		event.ErrorMessage = errMsg
	}
	l.LogEvent(event)
}

// LogResourceAllocation logs a resource allocation event.
func (l *Logger) LogResourceAllocation(ctx context.Context, poolID, subscriberID, ip string, success bool, errMsg string) {
	event := &Event{
		Type:         EventAllocationCreated,
		ResourceType: "allocation",
		PoolID:       poolID,
		SubscriberID: subscriberID,
		Success:      success,
		Metadata: map[string]string{
			"ip": ip,
		},
	}
	if !success {
		event.Type = EventAllocationConflict
		event.ErrorMessage = errMsg
	}
	l.LogEvent(event)
}

// LogResourceDeallocation logs a resource deallocation event.
func (l *Logger) LogResourceDeallocation(ctx context.Context, poolID, subscriberID string, success bool, errMsg string) {
	event := &Event{
		Type:         EventAllocationDeleted,
		ResourceType: "allocation",
		PoolID:       poolID,
		SubscriberID: subscriberID,
		Success:      success,
	}
	if !success {
		event.ErrorMessage = errMsg
	}
	l.LogEvent(event)
}

// LogPoolCreated logs a pool creation event.
func (l *Logger) LogPoolCreated(ctx context.Context, poolID, cidr string, actorID string) {
	l.LogEvent(&Event{
		Type:         EventPoolCreated,
		ResourceType: "pool",
		ResourceID:   poolID,
		PoolID:       poolID,
		ActorID:      actorID,
		Success:      true,
		Metadata: map[string]string{
			"cidr": cidr,
		},
	})
}

// LogPoolDeleted logs a pool deletion event.
func (l *Logger) LogPoolDeleted(ctx context.Context, poolID, actorID string) {
	l.LogEvent(&Event{
		Type:         EventPoolDeleted,
		ResourceType: "pool",
		ResourceID:   poolID,
		PoolID:       poolID,
		ActorID:      actorID,
		Success:      true,
	})
}

// LogConfigChange logs a configuration change event.
func (l *Logger) LogConfigChange(ctx context.Context, configKey, oldValue, newValue, actorID string) {
	l.LogEvent(&Event{
		Type:     EventConfigChange,
		ActorID:  actorID,
		Success:  true,
		OldValue: oldValue,
		NewValue: newValue,
		Metadata: map[string]string{
			"config_key": configKey,
		},
	})
}

// LogSuspiciousActivity logs a suspicious activity event.
func (l *Logger) LogSuspiciousActivity(ctx context.Context, threatType string, score int, sourceIP string, details string) {
	l.LogEvent(&Event{
		Type:         EventSuspiciousActivity,
		ThreatType:   threatType,
		ThreatScore:  score,
		Success:      false,
		ErrorMessage: details,
		Metadata: map[string]string{
			"source_ip": sourceIP,
		},
	})
}

// LogBruteForce logs a brute force detection event.
func (l *Logger) LogBruteForce(ctx context.Context, actorID, sourceIP string, failureCount int, blockedUntil time.Time) {
	l.LogEvent(&Event{
		Type:         EventBruteForceDetected,
		ActorID:      actorID,
		ThreatType:   "brute_force",
		FailureCount: failureCount,
		BlockedUntil: blockedUntil,
		Success:      false,
		Metadata: map[string]string{
			"source_ip": sourceIP,
		},
	})
}
