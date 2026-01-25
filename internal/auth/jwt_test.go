package auth

import (
	"context"
	"testing"
	"time"
)

func TestJWTParser_ParseToken(t *testing.T) {
	secret := []byte("test-secret-key-for-jwt-signing")

	config := DefaultConfig()
	config.JWTSecret = secret
	config.JWTIssuer = "test-issuer"
	config.JWTAudience = []string{"test-audience"}

	parser, err := NewJWTParser(config)
	if err != nil {
		t.Fatalf("NewJWTParser() error = %v", err)
	}

	t.Run("valid token", func(t *testing.T) {
		principal := &Principal{
			ID:          "user-123",
			Type:        PrincipalTypeUser,
			Roles:       []string{"reader", "writer"},
			Permissions: []string{"pools.read", "pools.write"},
		}

		token, err := parser.GenerateToken(principal, time.Hour)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		got, err := parser.ParseToken(context.Background(), token)
		if err != nil {
			t.Fatalf("ParseToken() error = %v", err)
		}

		if got.ID != "user-123" {
			t.Errorf("ParseToken() ID = %v, want user-123", got.ID)
		}

		if got.Type != PrincipalTypeUser {
			t.Errorf("ParseToken() Type = %v, want %v", got.Type, PrincipalTypeUser)
		}

		if len(got.Roles) != 2 {
			t.Errorf("ParseToken() Roles length = %d, want 2", len(got.Roles))
		}

		if len(got.Permissions) != 2 {
			t.Errorf("ParseToken() Permissions length = %d, want 2", len(got.Permissions))
		}
	})

	t.Run("expired token", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		// Generate token that expires immediately
		token, err := parser.GenerateToken(principal, -time.Hour)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		_, err = parser.ParseToken(context.Background(), token)
		if err != ErrTokenExpired {
			t.Errorf("ParseToken() error = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("invalid token format", func(t *testing.T) {
		_, err := parser.ParseToken(context.Background(), "invalid-token")
		if err == nil {
			t.Error("ParseToken() expected error for invalid format")
		}
	})

	t.Run("tampered token", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		token, err := parser.GenerateToken(principal, time.Hour)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		// Tamper with the token
		tampered := token[:len(token)-5] + "XXXXX"

		_, err = parser.ParseToken(context.Background(), tampered)
		if err == nil {
			t.Error("ParseToken() expected error for tampered token")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		// Create parser with strict issuer validation
		strictConfig := DefaultConfig()
		strictConfig.JWTSecret = secret
		strictConfig.JWTIssuer = "expected-issuer"

		strictParser, err := NewJWTParser(strictConfig)
		if err != nil {
			t.Fatalf("NewJWTParser() error = %v", err)
		}

		// Generate token with different issuer
		wrongIssuerConfig := DefaultConfig()
		wrongIssuerConfig.JWTSecret = secret
		wrongIssuerConfig.JWTIssuer = "wrong-issuer"

		wrongParser, err := NewJWTParser(wrongIssuerConfig)
		if err != nil {
			t.Fatalf("NewJWTParser() error = %v", err)
		}

		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		token, err := wrongParser.GenerateToken(principal, time.Hour)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		_, err = strictParser.ParseToken(context.Background(), token)
		if err == nil {
			t.Error("ParseToken() expected error for wrong issuer")
		}
	})
}

func TestJWTParser_RequiredClaims(t *testing.T) {
	secret := []byte("test-secret-key-for-jwt-signing")

	config := DefaultConfig()
	config.JWTSecret = secret
	config.JWTRequiredClaims = []string{"sub", "exp"}

	parser, err := NewJWTParser(config)
	if err != nil {
		t.Fatalf("NewJWTParser() error = %v", err)
	}

	t.Run("token with required claims", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		token, err := parser.GenerateToken(principal, time.Hour)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		_, err = parser.ParseToken(context.Background(), token)
		if err != nil {
			t.Errorf("ParseToken() error = %v", err)
		}
	})
}

func TestJWTParser_ClockSkew(t *testing.T) {
	secret := []byte("test-secret-key-for-jwt-signing")

	config := DefaultConfig()
	config.JWTSecret = secret
	config.JWTClockSkew = 10 * time.Minute

	parser, err := NewJWTParser(config)
	if err != nil {
		t.Fatalf("NewJWTParser() error = %v", err)
	}

	t.Run("token within clock skew tolerance", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		// Token that expired 5 minutes ago (within 10 minute clock skew)
		token, err := parser.GenerateToken(principal, -5*time.Minute)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		_, err = parser.ParseToken(context.Background(), token)
		if err != nil {
			t.Errorf("ParseToken() should accept token within clock skew, got error = %v", err)
		}
	})

	t.Run("token outside clock skew tolerance", func(t *testing.T) {
		principal := &Principal{
			ID:   "user-123",
			Type: PrincipalTypeUser,
		}

		// Token that expired 15 minutes ago (outside 10 minute clock skew)
		token, err := parser.GenerateToken(principal, -15*time.Minute)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}

		_, err = parser.ParseToken(context.Background(), token)
		if err != ErrTokenExpired {
			t.Errorf("ParseToken() error = %v, want ErrTokenExpired", err)
		}
	})
}

func TestJWTParser_NoKey(t *testing.T) {
	config := DefaultConfig()
	// No key configured

	_, err := NewJWTParser(config)
	if err == nil {
		t.Error("NewJWTParser() expected error when no key configured")
	}
}
