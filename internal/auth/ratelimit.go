package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter
type RateLimiter struct {
	mu           sync.RWMutex
	buckets      map[string]*tokenBucket
	maxRequests  int
	window       time.Duration
	byPrincipal  bool // true = rate limit by principal ID, false = by IP
	cleanupEvery time.Duration
	stopCleanup  chan struct{}
}

// tokenBucket implements token bucket rate limiting
type tokenBucket struct {
	tokens    int
	lastReset time.Time
}

// RateLimitConfig configures the rate limiter
type RateLimitConfig struct {
	MaxRequests  int           // Maximum requests per window
	Window       time.Duration // Time window for rate limiting
	ByPrincipal  bool          // Rate limit per principal (vs per IP)
	CleanupEvery time.Duration // How often to clean up old buckets
}

// DefaultRateLimitConfig returns sensible defaults
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		MaxRequests:  100,
		Window:       time.Minute,
		ByPrincipal:  false,
		CleanupEvery: 5 * time.Minute,
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	rl := &RateLimiter{
		buckets:      make(map[string]*tokenBucket),
		maxRequests:  config.MaxRequests,
		window:       config.Window,
		byPrincipal:  config.ByPrincipal,
		cleanupEvery: config.CleanupEvery,
		stopCleanup:  make(chan struct{}),
	}

	// Start cleanup goroutine
	if config.CleanupEvery > 0 {
		go rl.cleanupLoop()
	}

	return rl
}

// Allow checks if a request is allowed based on rate limits
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	bucket, exists := rl.buckets[key]
	if !exists {
		// New bucket with full tokens
		rl.buckets[key] = &tokenBucket{
			tokens:    rl.maxRequests - 1, // One token consumed
			lastReset: now,
		}
		return true
	}

	// Check if window has passed - reset tokens
	if now.Sub(bucket.lastReset) >= rl.window {
		bucket.tokens = rl.maxRequests - 1 // Reset and consume one
		bucket.lastReset = now
		return true
	}

	// Check if we have tokens
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	// No tokens left
	return false
}

// Remaining returns the number of remaining requests for a key
func (rl *RateLimiter) Remaining(key string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	bucket, exists := rl.buckets[key]
	if !exists {
		return rl.maxRequests
	}

	// Check if window has passed
	if time.Since(bucket.lastReset) >= rl.window {
		return rl.maxRequests
	}

	return bucket.tokens
}

// ResetTime returns when the rate limit resets for a key
func (rl *RateLimiter) ResetTime(key string) time.Time {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	bucket, exists := rl.buckets[key]
	if !exists {
		return time.Now().Add(rl.window)
	}

	return bucket.lastReset.Add(rl.window)
}

// Stop stops the cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCleanup)
}

// cleanupLoop periodically removes stale buckets
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCleanup:
			return
		}
	}
}

// cleanup removes buckets that haven't been used in the last window
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	threshold := time.Now().Add(-rl.window * 2) // Keep buckets for 2 windows

	for key, bucket := range rl.buckets {
		if bucket.lastReset.Before(threshold) {
			delete(rl.buckets, key)
		}
	}
}

// Middleware creates an HTTP middleware for rate limiting
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Determine the rate limit key
			key := rl.getKey(r)

			// Check rate limit
			if !rl.Allow(key) {
				// Set rate limit headers
				w.Header().Set("X-RateLimit-Limit", formatInt(rl.maxRequests))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", formatTime(rl.ResetTime(key)))
				w.Header().Set("Retry-After", formatInt(int(time.Until(rl.ResetTime(key)).Seconds())))

				http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", formatInt(rl.maxRequests))
			w.Header().Set("X-RateLimit-Remaining", formatInt(rl.Remaining(key)))
			w.Header().Set("X-RateLimit-Reset", formatTime(rl.ResetTime(key)))

			next.ServeHTTP(w, r)
		})
	}
}

// getKey determines the rate limit key for a request
func (rl *RateLimiter) getKey(r *http.Request) string {
	if rl.byPrincipal {
		// Try to get principal from context
		if principal := GetPrincipalFromContext(r.Context()); principal != nil {
			return "principal:" + principal.ID
		}
	}

	// Fall back to IP address
	return "ip:" + getClientIP(r)
}

// getClientIP extracts the client IP from a request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// formatInt converts an int to string
func formatInt(n int) string {
	return formatInt64(int64(n))
}

// formatTime formats time as Unix timestamp string
func formatTime(t time.Time) string {
	return formatInt64(t.Unix())
}

// formatInt64 converts an int64 to string
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}

	negative := false
	if n < 0 {
		negative = true
		n = -n
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	if negative {
		digits = append([]byte{'-'}, digits...)
	}

	return string(digits)
}

// RateLimitMiddleware creates a rate limiting middleware with the given configuration
func RateLimitMiddleware(config *RateLimitConfig) func(http.Handler) http.Handler {
	limiter := NewRateLimiter(config)
	return limiter.Middleware()
}

// EndpointRateLimiter provides per-endpoint rate limiting
type EndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	defaults *RateLimiter
	configs  map[string]*RateLimitConfig
}

// NewEndpointRateLimiter creates a rate limiter with per-endpoint configuration
func NewEndpointRateLimiter(defaultConfig *RateLimitConfig) *EndpointRateLimiter {
	if defaultConfig == nil {
		defaultConfig = DefaultRateLimitConfig()
	}

	return &EndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
		defaults: NewRateLimiter(defaultConfig),
		configs:  make(map[string]*RateLimitConfig),
	}
}

// SetEndpointLimit sets a custom rate limit for a specific endpoint
func (erl *EndpointRateLimiter) SetEndpointLimit(endpoint string, config *RateLimitConfig) {
	erl.configs[endpoint] = config
	erl.limiters[endpoint] = NewRateLimiter(config)
}

// Middleware creates middleware for per-endpoint rate limiting
func (erl *EndpointRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Find the most specific limiter for this endpoint
			limiter := erl.defaults
			for endpoint, rl := range erl.limiters {
				if r.URL.Path == endpoint || strings.HasPrefix(r.URL.Path, endpoint+"/") {
					limiter = rl
					break
				}
			}

			// Apply rate limiting
			key := limiter.getKey(r)

			if !limiter.Allow(key) {
				w.Header().Set("X-RateLimit-Limit", formatInt(limiter.maxRequests))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", formatTime(limiter.ResetTime(key)))
				w.Header().Set("Retry-After", formatInt(int(time.Until(limiter.ResetTime(key)).Seconds())))

				http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			w.Header().Set("X-RateLimit-Limit", formatInt(limiter.maxRequests))
			w.Header().Set("X-RateLimit-Remaining", formatInt(limiter.Remaining(key)))
			w.Header().Set("X-RateLimit-Reset", formatTime(limiter.ResetTime(key)))

			next.ServeHTTP(w, r)
		})
	}
}

// Stop stops all rate limiters
func (erl *EndpointRateLimiter) Stop() {
	erl.defaults.Stop()
	for _, limiter := range erl.limiters {
		limiter.Stop()
	}
}
