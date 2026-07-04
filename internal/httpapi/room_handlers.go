package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// RoomDeps bundles dependencies for the user-facing room handlers.
type RoomDeps struct {
	Store *postgres.Store
}

// ListRooms returns every room. In v1 all authenticated users see all rooms.
func (d RoomDeps) ListRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := d.Store.ListRooms(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list rooms")
		return
	}
	if rooms == nil {
		rooms = []models.Room{}
	}
	writeJSON(w, http.StatusOK, rooms)
}

// ListMessages returns a page of a room's message history, newest first.
//
// Query params:
//
//	limit  - max items to return (1..100, default 50)
//	before - ISO-8601 timestamp cursor; messages older than this are returned
//
// Because results are newest-first, a client fetches the next older page by
// passing the oldest message's created_at as `before`.
func (d RoomDeps) ListMessages(w http.ResponseWriter, r *http.Request) {
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

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid 'before' timestamp (use RFC 3339)")
			return
		}
		before = t
	}

	msgs, err := d.Store.ListMessages(r.Context(), roomID, before, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	if msgs == nil {
		msgs = []models.Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}
