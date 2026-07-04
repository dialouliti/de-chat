// Package models defines the core domain types shared across the application.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Role distinguishes administrators from regular users.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User is an authenticated chat participant. Accounts are created by an
// administrator; there is no self-registration.
type User struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"` // never serialized
	Role         Role      `json:"role"`
	Disabled     bool      `json:"disabled"` // true when an admin has blocked the user
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Room is a chat channel. In v1 every authenticated user can see and join
// every room.
type Room struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// Message is a single chat message persisted in a room.
type Message struct {
	ID        uuid.UUID `json:"id"`
	RoomID    uuid.UUID `json:"room_id"`
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"` // denormalized sender display name
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// --- WebSocket envelope types ---

// ClientMsg is a message sent by the client over the WebSocket.
type ClientMsg struct {
	Type    string    `json:"type"` // "message" | "ping"
	RoomID  uuid.UUID `json:"room_id,omitempty"`
	Content string    `json:"content,omitempty"`
}

// ServerMsg is a message sent by the server over the WebSocket.
type ServerMsg struct {
	Type      string    `json:"type"` // "message" | "pong" | "error"
	Message   *Message  `json:"message,omitempty"`
	MessageID uuid.UUID `json:"message_id,omitempty"` // for "error" responses
	Text      string    `json:"text,omitempty"`       // for "pong"/"error"
}
