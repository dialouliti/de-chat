package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// AdminDeps bundles dependencies for the admin handlers.
type AdminDeps struct {
	Store *postgres.Store
}

// createUserRequest is the body for POST /api/v1/admin/users.
type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"` // optional, defaults to "user"
}

// CreateUser creates a new user account (admin only).
func (d AdminDeps) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		jsonError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	role := models.RoleUser
	if req.Role != "" {
		role = models.Role(req.Role)
	}
	if role != models.RoleUser && role != models.RoleAdmin {
		jsonError(w, http.StatusBadRequest, "role must be 'user' or 'admin'")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user, err := d.Store.CreateUser(r.Context(), req.Username, hash, role)
	if err != nil {
		// A duplicate username is the most common conflict; surface it as 409.
		if isUniqueViolation(err) {
			jsonError(w, http.StatusConflict, "username already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

// ListUsers returns every user (admin only).
func (d AdminDeps) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := d.Store.ListUsers(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	if users == nil {
		users = []models.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

// DeleteUser removes a user by id (admin only).
func (d AdminDeps) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := d.Store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateRoomRequest is the body for POST /api/v1/rooms (admin only).
type CreateRoomRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateRoom creates a new room (admin only).
func (d AdminDeps) CreateRoom(w http.ResponseWriter, r *http.Request) {
	var req CreateRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}

	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	room, err := d.Store.CreateRoom(r.Context(), req.Name, req.Description, claims.UserID)
	if err != nil {
		if isUniqueViolation(err) {
			jsonError(w, http.StatusConflict, "room name already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create room")
		return
	}
	writeJSON(w, http.StatusCreated, room)
}

// DeleteRoom removes a room and its messages (admin only).
func (d AdminDeps) DeleteRoom(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := d.Store.DeleteRoom(r.Context(), id); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "room not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to delete room")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClearRoom deletes all messages in a room, keeping the room itself (admin only).
func (d AdminDeps) ClearRoom(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, err := d.Store.GetRoomByID(r.Context(), id); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "room not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to find room")
		return
	}
	if err := d.Store.ClearRoom(r.Context(), id); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to clear room")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BlockUser disables a user, immediately revoking their access. Existing JWTs
// stop working on the next request and the WebSocket handshake is rejected
// (active WS connections drop on reconnect).
func (d AdminDeps) BlockUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := d.Store.SetUserDisabled(r.Context(), id, true); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to block user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnblockUser re-enables a previously blocked user. They can log in again.
func (d AdminDeps) UnblockUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := d.Store.SetUserDisabled(r.Context(), id, false); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to unblock user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
