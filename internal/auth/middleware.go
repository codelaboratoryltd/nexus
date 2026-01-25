package auth

import (
	"net/http"

	"github.com/google/uuid"
)

// Middleware provides HTTP middleware for authentication
type Middleware struct {
	authenticator *Authenticator
}

// NewMiddleware creates a new authentication middleware
func NewMiddleware(authenticator *Authenticator) *Middleware {
	return &Middleware{
		authenticator: authenticator,
	}
}

// Handler returns an http.Handler that wraps the given handler with authentication
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate request ID if not present
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Add request ID to response header
		w.Header().Set("X-Request-ID", requestID)

		// Authenticate the request
		principal, err := m.authenticator.Authenticate(r)
		if err != nil {
			switch err {
			case ErrNoCredentials:
				http.Error(w, `{"error": "authentication required"}`, http.StatusUnauthorized)
			case ErrTokenExpired:
				http.Error(w, `{"error": "token expired"}`, http.StatusUnauthorized)
			case ErrInvalidCredentials, ErrTokenInvalid, ErrCertificateInvalid:
				http.Error(w, `{"error": "invalid credentials"}`, http.StatusUnauthorized)
			case ErrUnauthorized:
				http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			case ErrForbidden:
				http.Error(w, `{"error": "access forbidden"}`, http.StatusForbidden)
			default:
				http.Error(w, `{"error": "authentication failed"}`, http.StatusUnauthorized)
			}
			return
		}

		// Add principal to context
		ctx := r.Context()
		ctx = WithPrincipal(ctx, principal)

		// Add actor ID header for downstream logging
		if principal != nil {
			w.Header().Set("X-Actor-ID", principal.ID)
			w.Header().Set("X-Actor-Type", string(principal.Type))
		}

		// Call the next handler with the updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth is a middleware that requires authentication
func RequireAuth(authenticator *Authenticator) func(http.Handler) http.Handler {
	middleware := NewMiddleware(authenticator)
	return middleware.Handler
}

// RequireRole returns middleware that requires a specific role
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := GetPrincipalFromContext(r.Context())
			if principal == nil {
				http.Error(w, `{"error": "authentication required"}`, http.StatusUnauthorized)
				return
			}

			if !principal.HasRole(role) {
				http.Error(w, `{"error": "insufficient permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequirePermission returns middleware that requires a specific permission
func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := GetPrincipalFromContext(r.Context())
			if principal == nil {
				http.Error(w, `{"error": "authentication required"}`, http.StatusUnauthorized)
				return
			}

			if !principal.HasPermission(permission) {
				http.Error(w, `{"error": "insufficient permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAny returns middleware that requires at least one of the specified roles
func RequireAny(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := GetPrincipalFromContext(r.Context())
			if principal == nil {
				http.Error(w, `{"error": "authentication required"}`, http.StatusUnauthorized)
				return
			}

			for _, role := range roles {
				if principal.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, `{"error": "insufficient permissions"}`, http.StatusForbidden)
		})
	}
}

// RequireAll returns middleware that requires all of the specified roles
func RequireAll(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := GetPrincipalFromContext(r.Context())
			if principal == nil {
				http.Error(w, `{"error": "authentication required"}`, http.StatusUnauthorized)
				return
			}

			for _, role := range roles {
				if !principal.HasRole(role) {
					http.Error(w, `{"error": "insufficient permissions"}`, http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
