package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	config := &RateLimitConfig{
		MaxRequests:  5,
		Window:       time.Minute,
		CleanupEvery: 0, // Disable cleanup for testing
	}

	limiter := NewRateLimiter(config)
	defer limiter.Stop()

	t.Run("allows requests under limit", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			if !limiter.Allow("test-client") {
				t.Errorf("Allow() = false at request %d, want true", i+1)
			}
		}
	})

	t.Run("blocks requests over limit", func(t *testing.T) {
		// The previous 5 requests already consumed the tokens
		if limiter.Allow("test-client") {
			t.Error("Allow() = true, want false (over limit)")
		}
	})

	t.Run("different keys have separate limits", func(t *testing.T) {
		// A different client should have full quota
		if !limiter.Allow("other-client") {
			t.Error("Allow() = false for new client, want true")
		}
	})
}

func TestRateLimiter_WindowReset(t *testing.T) {
	config := &RateLimitConfig{
		MaxRequests:  2,
		Window:       50 * time.Millisecond,
		CleanupEvery: 0,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Stop()

	// Consume all tokens
	limiter.Allow("test-client")
	limiter.Allow("test-client")

	// Should be blocked
	if limiter.Allow("test-client") {
		t.Error("Allow() = true, want false (over limit)")
	}

	// Wait for window to reset
	time.Sleep(60 * time.Millisecond)

	// Should be allowed again
	if !limiter.Allow("test-client") {
		t.Error("Allow() = false after window reset, want true")
	}
}

func TestRateLimiter_Remaining(t *testing.T) {
	config := &RateLimitConfig{
		MaxRequests:  10,
		Window:       time.Minute,
		CleanupEvery: 0,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Stop()

	t.Run("full quota for new key", func(t *testing.T) {
		remaining := limiter.Remaining("new-client")
		if remaining != 10 {
			t.Errorf("Remaining() = %d, want 10", remaining)
		}
	})

	t.Run("decrements after requests", func(t *testing.T) {
		limiter.Allow("counter-client")
		limiter.Allow("counter-client")
		limiter.Allow("counter-client")

		remaining := limiter.Remaining("counter-client")
		if remaining != 7 {
			t.Errorf("Remaining() = %d, want 7", remaining)
		}
	})
}

func TestRateLimiter_Middleware(t *testing.T) {
	config := &RateLimitConfig{
		MaxRequests:  3,
		Window:       time.Minute,
		CleanupEvery: 0,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Stop()

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	t.Run("allows requests under limit", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/api/v1/test", nil)
			req.RemoteAddr = "192.168.1.1:12345"
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
			}

			// Check rate limit headers
			if w.Header().Get("X-RateLimit-Limit") == "" {
				t.Error("Missing X-RateLimit-Limit header")
			}
			if w.Header().Get("X-RateLimit-Remaining") == "" {
				t.Error("Missing X-RateLimit-Remaining header")
			}
		}
	})

	t.Run("blocks requests over limit with 429", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}

		// Check Retry-After header
		if w.Header().Get("Retry-After") == "" {
			t.Error("Missing Retry-After header")
		}
	})
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		xRealIP       string
		expectedIP    string
	}{
		{
			name:       "remote addr only",
			remoteAddr: "192.168.1.1:12345",
			expectedIP: "192.168.1.1",
		},
		{
			name:          "X-Forwarded-For single",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50",
			expectedIP:    "203.0.113.50",
		},
		{
			name:          "X-Forwarded-For multiple",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50, 70.41.3.18, 150.172.238.178",
			expectedIP:    "203.0.113.50",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			xRealIP:    "198.51.100.25",
			expectedIP: "198.51.100.25",
		},
		{
			name:          "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50",
			xRealIP:       "198.51.100.25",
			expectedIP:    "203.0.113.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}

			ip := getClientIP(req)
			if ip != tt.expectedIP {
				t.Errorf("getClientIP() = %v, want %v", ip, tt.expectedIP)
			}
		})
	}
}

func TestEndpointRateLimiter(t *testing.T) {
	defaultConfig := &RateLimitConfig{
		MaxRequests:  100,
		Window:       time.Minute,
		CleanupEvery: 0,
	}

	limiter := NewEndpointRateLimiter(defaultConfig)
	defer limiter.Stop()

	// Set lower limit for allocations endpoint
	limiter.SetEndpointLimit("/api/v1/allocations", &RateLimitConfig{
		MaxRequests:  2,
		Window:       time.Minute,
		CleanupEvery: 0,
	})

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("default limit for normal endpoint", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/api/v1/pools", nil)
			req.RemoteAddr = "192.168.1.1:12345"
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
			}
		}
	})

	t.Run("custom limit for allocations endpoint", func(t *testing.T) {
		// First 2 requests should succeed
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("POST", "/api/v1/allocations", nil)
			req.RemoteAddr = "192.168.1.2:12345" // Different IP from pools test
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
			}
		}

		// Third request should be rate limited
		req := httptest.NewRequest("POST", "/api/v1/allocations", nil)
		req.RemoteAddr = "192.168.1.2:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}
	})
}

func TestRateLimiter_ByPrincipal(t *testing.T) {
	config := &RateLimitConfig{
		MaxRequests:  2,
		Window:       time.Minute,
		ByPrincipal:  true,
		CleanupEvery: 0,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Stop()

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("rate limits by principal", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		// Make 2 requests with principal in context
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/api/v1/test", nil)
			req.RemoteAddr = "192.168.1.1:12345" // Same IP
			ctx := WithPrincipal(req.Context(), principal)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
			}
		}

		// Third request with same principal should be rate limited
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		ctx := WithPrincipal(req.Context(), principal)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}

		// Different principal should have separate limit
		differentPrincipal := &Principal{
			ID:   "user-456",
			Type: PrincipalTypeUser,
		}

		req = httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "192.168.1.1:12345" // Same IP
		ctx = WithPrincipal(req.Context(), differentPrincipal)
		req = req.WithContext(ctx)
		w = httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Different principal: status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestFormatInt64(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{1234567890, "1234567890"},
		{-1, "-1"},
		{-42, "-42"},
	}

	for _, tt := range tests {
		got := formatInt64(tt.input)
		if got != tt.want {
			t.Errorf("formatInt64(%d) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
