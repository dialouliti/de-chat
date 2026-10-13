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

// ListMessages returns a page of a room's message history.
//
// Query params:
//
//	limit  - max items to return (1..100, default 50)
//	before - ISO-8601 timestamp cursor; messages older than this are returned,
//	         newest first (the default order when no cursor is given)
//	after  - ISO-8601 timestamp cursor; messages newer than this are returned,
//	         oldest first — used to backfill after a WS reconnect
//
// `before` and `after` are mutually exclusive.
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

	limit, before, after, err := parseMessageListQuery(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	var msgs []models.Message
	if !after.IsZero() {
		msgs, err = d.Store.ListMessagesAfter(r.Context(), roomID, after, limit)
	} else {
		msgs, err = d.Store.ListMessages(r.Context(), roomID, before, limit)
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	if msgs == nil {
		msgs = []models.Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// parseMessageListQuery extracts the limit/before/after query parameters of
// GET /rooms/{id}/messages. limit keeps its historical behavior: an invalid
// value is silently ignored and the default of 50 applies. before and after
// are exclusive RFC 3339 cursors; passing both at once is an error.
func parseMessageListQuery(r *http.Request) (limit int, before, after time.Time, err error) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			limit = n
		}
	}

	beforeRaw := r.URL.Query().Get("before")
	if beforeRaw != "" {
		t, parseErr := time.Parse(time.RFC3339Nano, beforeRaw)
		if parseErr != nil {
			return 0, time.Time{}, time.Time{}, errors.New("invalid 'before' timestamp (use RFC 3339)")
		}
		before = t
	}

	afterRaw := r.URL.Query().Get("after")
	if afterRaw != "" {
		t, parseErr := time.Parse(time.RFC3339Nano, afterRaw)
		if parseErr != nil {
			return 0, time.Time{}, time.Time{}, errors.New("invalid 'after' timestamp (use RFC 3339)")
		}
		after = t
	}

	if beforeRaw != "" && afterRaw != "" {
		return 0, time.Time{}, time.Time{}, errors.New("cannot combine 'before' and 'after'")
	}

	return limit, before, after, nil
}
