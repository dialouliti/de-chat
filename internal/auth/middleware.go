package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type contextKey string

// CtxUser is the context key holding the authenticated *Claims.
const CtxUser contextKey = "user"

// UserChecker reports whether a user should be considered active for the
// purpose of request authorization. Returning disabled=true (or any error)
// causes the request to be rejected. It is injected from the store layer so
// the auth package has no dependency on postgres.
type UserChecker func(ctx context.Context, userID uuid.UUID) (disabled bool, err error)

// noOpChecker is the default: nobody is disabled. Used until SetUserChecker
// wires in the real store-backed implementation.
func noOpChecker(context.Context, uuid.UUID) (bool, error) { return false, nil }

// FromContext extracts the authenticated claims from a request context, if present.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(CtxUser).(*Claims)
	return c, ok
}

// RequireAuth returns middleware that validates the Bearer token and stores the
// claims in the request context. Unauthenticated requests get 401. A user that
// has been disabled (blocked by an admin) is rejected here too, so blocking
// takes effect immediately even for users holding a still-valid JWT.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, ok := bearerToken(r)
		if !ok {
			http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}
		claims, err := s.Authenticate(r.Context(), tokenStr)
		if err != nil {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), CtxUser, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin returns middleware that allows only admin-role requests. It must
// run after RequireAuth. Non-admins get 403.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := FromContext(r.Context())
		if !ok || claims.Role != "admin" {
			http.Error(w, "admin privileges required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token portion from an "Authorization: Bearer <t>"
// header. The boolean is false when absent or malformed.
func bearerToken(r *http.Request) (string, bool) {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(v[len(prefix):]), true
}

// UserIDFromContext is a convenience helper used by handlers.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	c, ok := FromContext(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return c.UserID, true
}
