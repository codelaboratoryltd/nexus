package audit

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Middleware is an HTTP middleware that logs API access events.
type Middleware struct {
	logger *Logger
}

// NewMiddleware creates a new audit middleware.
func NewMiddleware(logger *Logger) *Middleware {
	return &Middleware{logger: logger}
}

// Handler returns an http.Handler that wraps the given handler with audit logging.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate request ID if not present
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Create a response writer wrapper to capture status code
		wrapper := &responseWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		// Call the next handler
		next.ServeHTTP(wrapper, r)

		// Calculate duration
		duration := time.Since(start)

		// Build audit event
		event := &Event{
			Type:        EventAPIAuthSuccess,
			RequestID:   requestID,
			SourceIP:    parseIP(r.RemoteAddr),
			UserAgent:   r.UserAgent(),
			APIEndpoint: r.URL.Path,
			HTTPMethod:  r.Method,
			HTTPStatus:  wrapper.statusCode,
			Success:     wrapper.statusCode < 400,
			Metadata: map[string]string{
				"duration_ms": formatDuration(duration),
				"query":       r.URL.RawQuery,
			},
		}

		// Get actor from header or context (customize based on auth mechanism)
		if actor := r.Header.Get("X-Actor-ID"); actor != "" {
			event.ActorID = actor
			event.ActorType = r.Header.Get("X-Actor-Type")
			if event.ActorType == "" {
				event.ActorType = "user"
			}
		}

		// Determine event type based on status code
		switch {
		case wrapper.statusCode == http.StatusUnauthorized:
			event.Type = EventAPIAuthFailure
			event.Success = false
		case wrapper.statusCode == http.StatusForbidden:
			event.Type = EventAPIAccessDenied
			event.Success = false
		case wrapper.statusCode == http.StatusTooManyRequests:
			event.Type = EventAPIRateLimited
			event.Success = false
		case wrapper.statusCode >= 400:
			event.Type = EventAPIAuthAttempt
			event.Success = false
		}

		m.logger.LogEvent(event)
	})
}

// responseWrapper wraps http.ResponseWriter to capture the status code.
type responseWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// parseIP extracts the IP address from a remote address string.
func parseIP(remoteAddr string) net.IP {
	// Handle IPv6 addresses in brackets
	if strings.HasPrefix(remoteAddr, "[") {
		idx := strings.LastIndex(remoteAddr, "]")
		if idx > 0 {
			remoteAddr = remoteAddr[1:idx]
		}
	} else {
		// Strip port from IPv4 address
		if idx := strings.LastIndex(remoteAddr, ":"); idx > 0 {
			remoteAddr = remoteAddr[:idx]
		}
	}

	return net.ParseIP(remoteAddr)
}

// formatDuration formats a duration as milliseconds string.
func formatDuration(d time.Duration) string {
	return strings.TrimSuffix(d.Round(time.Millisecond).String(), "ms")
}
