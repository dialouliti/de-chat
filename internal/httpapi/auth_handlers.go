package httpapi

import (
	"errors"
	"net/http"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// AuthDeps bundles the dependencies for the auth handlers.
type AuthDeps struct {
	Store *postgres.Store
	Auth  *auth.Service
}

// loginRequest is the body for POST /api/v1/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse carries the JWT and the basic user info.
type loginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Login authenticates a user with username + password and returns a JWT.
func (d AuthDeps) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		jsonError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	user, err := d.Store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			// Run a dummy bcrypt compare so this path takes roughly the same
			// time as the real one below; otherwise the response-time gap
			// would let an attacker enumerate valid usernames. The result is
			// irrelevant and intentionally discarded.
			_ = auth.DummyCompare(req.Password)
			jsonError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		jsonError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := auth.ComparePassword(user.PasswordHash, req.Password); err != nil {
		jsonError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// A blocked user cannot log in, even with correct credentials.
	if user.Disabled {
		jsonError(w, http.StatusUnauthorized, "account is blocked")
		return
	}

	token, err := d.Auth.IssueToken(user.ID, user.Username, string(user.Role))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:    token,
		Username: user.Username,
		Role:     string(user.Role),
	})
}

// Me returns the authenticated user's profile from the JWT claims.
func (d AuthDeps) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":  claims.UserID,
		"username": claims.Username,
		"role":     claims.Role,
	})
}

// MeUnread returns the authenticated user's unread message count per room:
// other users' messages created after the user's last-read cursor. Rooms with
// nothing unread are omitted, so the response may be an empty array.
func (d AuthDeps) MeUnread(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	unread, err := d.Store.UnreadCounts(r.Context(), claims.UserID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to count unread messages")
		return
	}
	if unread == nil {
		unread = []models.RoomUnread{}
	}
	writeJSON(w, http.StatusOK, unread)
}
