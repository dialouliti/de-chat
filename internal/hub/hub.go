// Package hub maintains the set of connected WebSocket clients and broadcasts
// messages to the clients that should receive them.
package hub

import (
	"log/slog"

	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/models"
)

// Client is the minimal contract the hub needs from a WS client. The methods
// are exported so a type defined in another package (internal/ws) can satisfy
// the interface.
type Client interface {
	UserID() uuid.UUID
	Send(models.ServerMsg)
}

// Hub is safe for concurrent use. Register/Unregister and Broadcast are all
// funneled through a single goroutine reading the channels, so no locking is
// needed around the client set.
type Hub struct {
	register   chan Client
	unregister chan Client
	broadcast  chan broadcast

	log *slog.Logger
}

// broadcast pairs a room-scoped message with the rooms it should reach.
type broadcast struct {
	rooms   []uuid.UUID // which rooms the message belongs to
	message models.ServerMsg
}

// New creates a Hub.
func New(log *slog.Logger) *Hub {
	return &Hub{
		register:   make(chan Client),
		unregister: make(chan Client),
		broadcast:  make(chan broadcast, 256),
		log:        log,
	}
}

// Register adds a client to the active set.
func (h *Hub) Register(c Client) { h.register <- c }

// Unregister removes a client from the active set.
func (h *Hub) Unregister(c Client) { h.unregister <- c }

// Broadcast sends a message to every client currently connected to any of the
// given rooms. Because every user can see every room, a client is considered
// "in" a room if it is connected; the rooms filter is kept so a future,
// membership-based model can plug in without changing the call sites.
func (h *Hub) Broadcast(rooms []uuid.UUID, message models.ServerMsg) {
	h.broadcast <- broadcast{rooms: rooms, message: message}
}

// Run processes hub events until ctxDone is closed. It must run as a
// long-lived goroutine for the lifetime of the server.
func (h *Hub) Run(ctxDone <-chan struct{}) {
	// clients maps a connected client to a set of room ids it is in. With the
	// v1 "all users see all rooms" rule a client is conceptually in every
	// room; we store the union of room ids we have seen so broadcast matching
	// still works once membership is introduced.
	clients := make(map[Client]map[uuid.UUID]struct{})

	for {
		select {
		case <-ctxDone:
			return
		case c := <-h.register:
			clients[c] = make(map[uuid.UUID]struct{})
			h.log.Debug("client registered", "user", c.UserID(), "clients", len(clients))
		case c := <-h.unregister:
			if _, ok := clients[c]; ok {
				delete(clients, c)
				h.log.Debug("client unregistered", "user", c.UserID(), "clients", len(clients))
			}
		case b := <-h.broadcast:
			h.deliver(clients, b)
		}
	}
}

// deliver fans a message out to every client whose room set intersects b.rooms.
// When b.rooms is empty (defensive fallback) nobody receives it.
func (h *Hub) deliver(clients map[Client]map[uuid.UUID]struct{}, b broadcast) {
	if len(b.rooms) == 0 {
		return
	}
	want := make(map[uuid.UUID]struct{}, len(b.rooms))
	for _, r := range b.rooms {
		want[r] = struct{}{}
	}
	for c := range clients {
		if roomIntersect(clients[c], want) {
			c.Send(b.message)
		}
	}
}

func roomIntersect(have, want map[uuid.UUID]struct{}) bool {
	if len(have) == 0 {
		// v1: a connected client with no explicit room membership still
		// receives everything (all users see all rooms).
		return true
	}
	for r := range want {
		if _, ok := have[r]; ok {
			return true
		}
	}
	return false
}
