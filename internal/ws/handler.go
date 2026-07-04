// Package ws implements the WebSocket endpoint: upgrading connections,
// reading client messages, persisting them, and broadcasting via the hub.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/hub"
	"github.com/pavelananev/dechat/internal/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same-origin is not enforced because clients may run anywhere; auth is
	// handled via the JWT query parameter before upgrading.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	sendChanSize   = 256
	maxMessageSize = 16 * 1024 // 16 KiB; text messages in v1
)

// MessageSaver persists a message and returns the stored row. It is a thin
// wrapper around *postgres.Store to keep this package testable.
type MessageSaver interface {
	CreateMessage(ctx context.Context, roomID, userID uuid.UUID, username, content string) (models.Message, error)
	GetRoomByID(ctx context.Context, id uuid.UUID) (models.Room, error)
}

// Handler is the HTTP handler for GET /api/v1/ws.
type Handler struct {
	hub   *hub.Hub
	store MessageSaver
	auth  *auth.Service
	log   *slog.Logger
}

// New creates a WS handler.
func New(h *hub.Hub, store MessageSaver, authSvc *auth.Service, log *slog.Logger) *Handler {
	return &Handler{hub: h, store: store, auth: authSvc, log: log}
}

// ServeHTTP validates the JWT in the ?token= query param and upgrades.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	claims, err := h.auth.Authenticate(r.Context(), tokenStr)
	if err != nil {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response; nothing more to do.
		h.log.Warn("ws upgrade failed", "error", err, "user", claims.Username)
		return
	}

	c := newClient(conn, h.hub, h.store, h.auth, claims, h.log)
	h.hub.Register(c)
	go c.writePump()
	go c.readPump()
}

// client is a single WebSocket connection.
type client struct {
	conn    *websocket.Conn
	hub     *hub.Hub
	store   MessageSaver
	auth    *auth.Service
	claims  *auth.Claims
	log     *slog.Logger
	outbox  chan models.ServerMsg
	closeMu sync.Once
}

func newClient(conn *websocket.Conn, h *hub.Hub, store MessageSaver, authSvc *auth.Service, claims *auth.Claims, log *slog.Logger) *client {
	return &client{
		conn:   conn,
		hub:    h,
		store:  store,
		auth:   authSvc,
		claims: claims,
		log:    log,
		outbox: make(chan models.ServerMsg, sendChanSize),
	}
}

func (c *client) UserID() uuid.UUID { return c.claims.UserID }

// Send pushes a message to the write pump. If the buffer is full the client is
// dropped — a slow consumer should not stall the hub.
func (c *client) Send(m models.ServerMsg) {
	defer func() {
		// A send on a closed channel panics; recover so a concurrent
		// unregister never crashes the hub goroutine.
		_ = recover()
	}()
	select {
	case c.outbox <- m:
	default:
		go c.close()
	}
}

// close shuts the connection down exactly once.
func (c *client) close() {
	c.closeMu.Do(func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	})
}

// readPump reads messages from the client until the connection drops.
func (c *client) readPump() {
	defer c.close()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.log.Debug("ws read closed", "user", c.claims.Username, "error", err)
			}
			return
		}

		var in models.ClientMsg
		if err := json.Unmarshal(payload, &in); err != nil {
			c.sendErr(uuid.Nil, "invalid message format")
			continue
		}
		c.handle(in)
	}
}

// handle dispatches a single decoded client message.
func (c *client) handle(in models.ClientMsg) {
	switch in.Type {
	case "ping":
		c.Send(models.ServerMsg{Type: "pong", Text: "pong"})
	case "message":
		c.handleMessage(in)
	default:
		c.sendErr(uuid.Nil, "unknown message type")
	}
}

// handleMessage persists a chat message and broadcasts it to the room.
func (c *client) handleMessage(in models.ClientMsg) {
	if in.Content == "" {
		c.sendErr(in.RoomID, "content is required")
		return
	}
	if in.RoomID == uuid.Nil {
		c.sendErr(uuid.Nil, "room_id is required")
		return
	}
	if _, err := c.store.GetRoomByID(context.Background(), in.RoomID); err != nil {
		c.sendErr(in.RoomID, "room not found")
		return
	}

	msg, err := c.store.CreateMessage(context.Background(), in.RoomID, c.claims.UserID, c.claims.Username, in.Content)
	if err != nil {
		c.log.Error("persist message", "error", err, "user", c.claims.Username)
		c.sendErr(in.RoomID, "failed to store message")
		return
	}

	c.hub.Broadcast(
		[]uuid.UUID{in.RoomID},
		models.ServerMsg{Type: "message", Message: &msg},
	)
}

// sendErr pushes a client-local error; it is not broadcast to others.
func (c *client) sendErr(messageID uuid.UUID, text string) {
	c.Send(models.ServerMsg{Type: "error", MessageID: messageID, Text: text})
}

// writePump drains the send channel to the connection and issues periodic
// pings to keep the link alive.
func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case m, ok := <-c.outbox:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteJSON(m); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
