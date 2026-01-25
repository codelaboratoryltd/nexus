package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_Handler(t *testing.T) {
	validator := NewStaticAPIKeyValidator()
	validator.AddKey("valid-key", &Principal{
		ID:    "test-user",
		Type:  PrincipalTypeUser,
		Roles: []string{"reader"},
	})

	config := DefaultConfig()
	config.APIKeyEnabled = true
	config.APIKeyValidator = validator
	config.AnonymousEndpoints = []string{"/health"}

	authenticator, err := NewAuthenticator(config)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}

	middleware := NewMiddleware(authenticator)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := GetPrincipalFromContext(r.Context())
		if principal != nil {
			w.Header().Set("X-Principal-ID", principal.ID)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	t.Run("authenticated request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		req.Header.Set("X-API-Key", "valid-key")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		if w.Header().Get("X-Principal-ID") != "test-user" {
			t.Errorf("X-Principal-ID = %v, want test-user", w.Header().Get("X-Principal-ID"))
		}

		if w.Header().Get("X-Request-ID") == "" {
			t.Error("Missing X-Request-ID header")
		}
	})

	t.Run("unauthenticated request to protected endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("anonymous endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("invalid credentials", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		req.Header.Set("X-API-Key", "invalid-key")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("preserves custom request ID", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		req.Header.Set("X-API-Key", "valid-key")
		req.Header.Set("X-Request-ID", "custom-request-id-123")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Header().Get("X-Request-ID") != "custom-request-id-123" {
			t.Errorf("X-Request-ID = %v, want custom-request-id-123", w.Header().Get("X-Request-ID"))
		}
	})
}

func TestRequireRole(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admin only"))
	}))

	t.Run("principal with required role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "admin-user",
			Type:  PrincipalTypeUser,
			Roles: []string{"admin"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("principal without required role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "regular-user",
			Type:  PrincipalTypeUser,
			Roles: []string{"reader"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("no principal", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/admin", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})
}

func TestRequirePermission(t *testing.T) {
	handler := RequirePermission("pools.write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("principal with required permission", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/pools", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:          "writer-user",
			Type:        PrincipalTypeUser,
			Permissions: []string{"pools.read", "pools.write"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("principal without required permission", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/pools", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:          "reader-user",
			Type:        PrincipalTypeUser,
			Permissions: []string{"pools.read"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})
}

func TestRequireAny(t *testing.T) {
	handler := RequireAny("admin", "operator")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("principal with first role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "admin-user",
			Roles: []string{"admin"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("principal with second role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "operator-user",
			Roles: []string{"operator"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("principal with neither role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "viewer-user",
			Roles: []string{"viewer"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})
}

func TestRequireAll(t *testing.T) {
	handler := RequireAll("reader", "writer")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("principal with all roles", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "full-user",
			Roles: []string{"reader", "writer"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("principal missing one role", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := WithPrincipal(req.Context(), &Principal{
			ID:    "partial-user",
			Roles: []string{"reader"},
		})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})
}
