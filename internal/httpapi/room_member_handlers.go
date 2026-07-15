package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// RoomMemberDeps holds deps for the room-member REST handlers.
type RoomMemberDeps struct {
	Store *postgres.Store
}

// addMemberRequest is the body for POST /api/v1/rooms/{id}/members.
type addMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"` // "member" (default) or "owner"
}

// ListMembers returns every member of a room. Any authenticated user can view.
func (d RoomMemberDeps) ListMembers(w http.ResponseWriter, r *http.Request) {
	roomID, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, err := d.Store.GetRoomByID(r.Context(), roomID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "room not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to find room")
		return
	}

	members, err := d.Store.ListRoomMembers(r.Context(), roomID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	if members == nil {
		members = []models.RoomMember{}
	}
	writeJSON(w, http.StatusOK, members)
}

// AddMember adds or updates a member's role. Owner or admin only.
func (d RoomMemberDeps) AddMember(w http.ResponseWriter, r *http.Request) {
	roomID, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !canManageRoom(r.Context(), d.Store, roomID, claims) {
		jsonError(w, http.StatusForbidden, "only the room owner or an admin can manage members")
		return
	}

	var req addMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	targetUserID, ok := parseUUID(w, req.UserID)
	if !ok {
		return
	}
	role := models.RoomRoleMember
	if req.Role != "" {
		role = models.RoomRole(req.Role)
	}
	if role != models.RoomRoleMember && role != models.RoomRoleOwner {
		jsonError(w, http.StatusBadRequest, "role must be 'member' or 'owner'")
		return
	}

	if err := d.Store.AddRoomMember(r.Context(), roomID, targetUserID, role); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to add member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveMember removes a member. Owner or admin only. An owner may be removed
// only by an admin (so a room is never left without an owner by its own owner).
func (d RoomMemberDeps) RemoveMember(w http.ResponseWriter, r *http.Request) {
	roomID, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	targetUserID, ok := parseUUID(w, chi.URLParam(r, "userId"))
	if !ok {
		return
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !canManageRoom(r.Context(), d.Store, roomID, claims) {
		jsonError(w, http.StatusForbidden, "only the room owner or an admin can manage members")
		return
	}

	// A non-admin owner cannot remove another owner.
	if claims.Role != string(models.RoleAdmin) {
		if targetRole, err := d.Store.GetRoomRole(r.Context(), roomID, targetUserID); err == nil && targetRole == models.RoomRoleOwner {
			jsonError(w, http.StatusForbidden, "only an admin can remove an owner")
			return
		}
	}

	if err := d.Store.RemoveRoomMember(r.Context(), roomID, targetUserID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "member not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// canManageRoom reports whether the caller is the room's owner or a global
// admin. Used to gate member-management, room deletion, and room clearing.
func canManageRoom(ctx context.Context, store *postgres.Store, roomID uuid.UUID, claims *auth.Claims) bool {
	if claims.Role == string(models.RoleAdmin) {
		return true
	}
	role, err := store.GetRoomRole(ctx, roomID, claims.UserID)
	if err != nil {
		return false
	}
	return role == models.RoomRoleOwner
}
