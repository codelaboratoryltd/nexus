package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrincipal_HasRole(t *testing.T) {
	tests := []struct {
		name      string
		principal *Principal
		role      string
		want      bool
	}{
		{
			name: "has exact role",
			principal: &Principal{
				Roles: []string{"reader", "writer"},
			},
			role: "writer",
			want: true,
		},
		{
			name: "does not have role",
			principal: &Principal{
				Roles: []string{"reader"},
			},
			role: "writer",
			want: false,
		},
		{
			name: "admin has all roles",
			principal: &Principal{
				Roles: []string{"admin"},
			},
			role: "writer",
			want: true,
		},
		{
			name: "empty roles",
			principal: &Principal{
				Roles: []string{},
			},
			role: "reader",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.HasRole(tt.role); got != tt.want {
				t.Errorf("HasRole() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrincipal_HasPermission(t *testing.T) {
	tests := []struct {
		name       string
		principal  *Principal
		permission string
		want       bool
	}{
		{
			name: "has exact permission",
			principal: &Principal{
				Permissions: []string{"pools.read", "pools.write"},
			},
			permission: "pools.write",
			want:       true,
		},
		{
			name: "does not have permission",
			principal: &Principal{
				Permissions: []string{"pools.read"},
			},
			permission: "pools.write",
			want:       false,
		},
		{
			name: "wildcard permission",
			principal: &Principal{
				Permissions: []string{"*"},
			},
			permission: "pools.write",
			want:       true,
		},
		{
			name: "admin role has all permissions",
			principal: &Principal{
				Roles:       []string{"admin"},
				Permissions: []string{},
			},
			permission: "pools.write",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStaticAPIKeyValidator(t *testing.T) {
	validator := NewStaticAPIKeyValidator()

	// Add a key
	principal := &Principal{
		ID:    "test-service",
		Type:  PrincipalTypeService,
		Roles: []string{"service"},
	}
	validator.AddKey("test-api-key-123", principal)

	t.Run("valid key", func(t *testing.T) {
		got, err := validator.ValidateKey(context.Background(), "test-api-key-123")
		if err != nil {
			t.Fatalf("ValidateKey() error = %v", err)
		}
		if got.ID != "test-service" {
			t.Errorf("ValidateKey() ID = %v, want test-service", got.ID)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		_, err := validator.ValidateKey(context.Background(), "wrong-key")
		if err != ErrInvalidCredentials {
			t.Errorf("ValidateKey() error = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("expired key", func(t *testing.T) {
		expiredPrincipal := &Principal{
			ID:        "expired-service",
			Type:      PrincipalTypeService,
			ExpiresAt: time.Now().Add(-time.Hour),
		}
		validator.AddKey("expired-key", expiredPrincipal)

		_, err := validator.ValidateKey(context.Background(), "expired-key")
		if err != ErrTokenExpired {
			t.Errorf("ValidateKey() error = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("remove key", func(t *testing.T) {
		validator.RemoveKey("test-api-key-123")
		_, err := validator.ValidateKey(context.Background(), "test-api-key-123")
		if err != ErrInvalidCredentials {
			t.Errorf("ValidateKey() error = %v, want ErrInvalidCredentials", err)
		}
	})
}

func TestStaticAPIKeyValidator_KeysAreHashed(t *testing.T) {
	validator := NewStaticAPIKeyValidator()
	plaintext := "secret-api-key-456"

	validator.AddKey(plaintext, &Principal{
		ID:   "hash-test",
		Type: PrincipalTypeService,
	})

	// The internal map should not contain the plaintext key
	validator.mu.RLock()
	defer validator.mu.RUnlock()
	for storedKey := range validator.keys {
		if storedKey == plaintext {
			t.Error("plaintext API key found in storage; expected SHA-256 hash")
		}
		// SHA-256 hex digest is 64 characters of hex
		if len(storedKey) != 64 {
			t.Errorf("stored key length = %d, want 64 (SHA-256 hex)", len(storedKey))
		}
		// Should be valid hex
		for _, c := range storedKey {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("stored key contains non-hex character %q", c)
				break
			}
		}
	}
}

func TestAuthenticator_APIKey(t *testing.T) {
	validator := NewStaticAPIKeyValidator()
	validator.AddKey("valid-api-key", &Principal{
		ID:    "test-service",
		Type:  PrincipalTypeService,
		Roles: []string{"service"},
	})

	config := DefaultConfig()
	config.APIKeyEnabled = true
	config.APIKeyValidator = validator

	auth, err := NewAuthenticator(config)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}

	t.Run("valid API key in header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		req.Header.Set("X-API-Key", "valid-api-key")

		principal, err := auth.Authenticate(req)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if principal.ID != "test-service" {
			t.Errorf("Authenticate() ID = %v, want test-service", principal.ID)
		}
	})

	t.Run("invalid API key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)
		req.Header.Set("X-API-Key", "invalid-key")

		_, err := auth.Authenticate(req)
		if err == nil {
			t.Error("Authenticate() expected error for invalid key")
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)

		_, err := auth.Authenticate(req)
		if err != ErrNoCredentials {
			t.Errorf("Authenticate() error = %v, want ErrNoCredentials", err)
		}
	})
}

func TestAuthenticator_AnonymousEndpoints(t *testing.T) {
	config := DefaultConfig()
	config.AnonymousEndpoints = []string{"/health", "/ready"}

	auth, err := NewAuthenticator(config)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}

	t.Run("anonymous endpoint allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)

		principal, err := auth.Authenticate(req)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if principal != nil {
			t.Error("Authenticate() expected nil principal for anonymous endpoint")
		}
	})

	t.Run("protected endpoint requires auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/pools", nil)

		_, err := auth.Authenticate(req)
		if err != ErrNoCredentials {
			t.Errorf("Authenticate() error = %v, want ErrNoCredentials", err)
		}
	})
}

func TestContextFunctions(t *testing.T) {
	principal := &Principal{
		ID:    "test-user",
		Type:  PrincipalTypeUser,
		Roles: []string{"reader"},
	}

	ctx := context.Background()

	t.Run("no principal in context", func(t *testing.T) {
		got := GetPrincipalFromContext(ctx)
		if got != nil {
			t.Error("GetPrincipalFromContext() expected nil")
		}
	})

	t.Run("principal in context", func(t *testing.T) {
		ctxWithPrincipal := WithPrincipal(ctx, principal)
		got := GetPrincipalFromContext(ctxWithPrincipal)
		if got == nil {
			t.Fatal("GetPrincipalFromContext() returned nil")
		}
		if got.ID != "test-user" {
			t.Errorf("GetPrincipalFromContext() ID = %v, want test-user", got.ID)
		}
	})
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.JWTClockSkew != 5*time.Minute {
		t.Errorf("JWTClockSkew = %v, want 5m", config.JWTClockSkew)
	}

	if config.APIKeyHeader != "X-API-Key" {
		t.Errorf("APIKeyHeader = %v, want X-API-Key", config.APIKeyHeader)
	}

	if len(config.AnonymousEndpoints) != 2 {
		t.Errorf("AnonymousEndpoints length = %d, want 2", len(config.AnonymousEndpoints))
	}
}
