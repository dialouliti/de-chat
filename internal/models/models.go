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

// RoomRole is a per-room role (moderation-only; does not affect visibility).
type RoomRole string

const (
	RoomRoleOwner  RoomRole = "owner"
	RoomRoleMember RoomRole = "member"
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
// every room; the room_members table records ownership for moderation only.
type Room struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// Message is a single chat message persisted in a room.
type Message struct {
	ID        uuid.UUID  `json:"id"`
	RoomID    uuid.UUID  `json:"room_id"`
	UserID    uuid.UUID  `json:"user_id"`
	Username  string     `json:"username"` // denormalized sender display name
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at,omitempty"` // nil until first edit
}

// ReadReceipt is a single user's last-read cursor for a room.
type ReadReceipt struct {
	UserID     uuid.UUID `json:"user_id"`
	Username   string    `json:"username"`
	LastReadAt time.Time `json:"last_read_at"`
}

// RoomMember is a (room, user) membership row with a per-room role.
type RoomMember struct {
	RoomID   uuid.UUID `json:"room_id"`
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
	Role     RoomRole  `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// --- WebSocket envelope types ---

// ClientMsg is a message sent by the client over the WebSocket.
type ClientMsg struct {
	Type     string    `json:"type"` // "message" | "ping" | "typing" | "read" | "edit"
	RoomID   uuid.UUID `json:"room_id,omitempty"`
	MessageID uuid.UUID `json:"message_id,omitempty"`
	Content  string    `json:"content,omitempty"`
	IsTyping bool      `json:"is_typing,omitempty"` // for "typing"
}

// ServerMsg is a message sent by the server over the WebSocket.
type ServerMsg struct {
	Type      string        `json:"type"` // "message" | "pong" | "error" | "typing" | "presence" | "read_receipt" | "message_edited"
	Message   *Message      `json:"message,omitempty"`
	MessageID uuid.UUID     `json:"message_id,omitempty"` // for "error" / "message_edited"
	Text      string        `json:"text,omitempty"`       // for "pong"/"error"

	// Actor + room scope for ephemeral events. Carried at the envelope level so
	// clients can filter by room_id without a nested *Message.
	UserID   uuid.UUID `json:"user_id,omitempty"`
	Username string    `json:"username,omitempty"`
	RoomID   uuid.UUID `json:"room_id,omitempty"`

	// Event-specific flags.
	IsTyping bool          `json:"is_typing,omitempty"` // "typing"
	IsOnline bool          `json:"is_online,omitempty"` // "presence"
	OnlineUsers []PresenceUser `json:"online_users,omitempty"` // presence snapshot sent to a newcomer
	ReadBy   []ReadReceipt `json:"read_by,omitempty"`   // "read_receipt"
}

// PresenceUser is a lightweight {id, name} pair used in presence snapshots.
// We avoid serializing the whole User (which would carry role/disabled/timestamps)
// and avoid requiring a full store fetch for every connect.
type PresenceUser struct {
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
}
