package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/models"
)

// UpsertReadState marks a room as read by userID at the current time. If a row
// already exists it is advanced forward; otherwise one is inserted.
func (s *Store) UpsertReadState(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `
		INSERT INTO message_read_state (room_id, user_id, last_read_at)
		VALUES ($1, $2, now())
		ON CONFLICT (room_id, user_id) DO UPDATE
			SET last_read_at = now()
			WHERE message_read_state.last_read_at < now()`
	_, err := s.Pool.Exec(ctx, q, roomID, userID)
	if err != nil {
		return fmt.Errorf("upsert read state: %w", err)
	}
	return nil
}

// ReadReceipts returns the last-read cursor for every user in a room who has
// one. Used to derive "read by N" for a given message.
func (s *Store) ReadReceipts(ctx context.Context, roomID uuid.UUID) ([]models.ReadReceipt, error) {
	const q = `
		SELECT rs.user_id, u.username, rs.last_read_at
		FROM message_read_state rs
		JOIN users u ON u.id = rs.user_id
		WHERE rs.room_id = $1
		ORDER BY rs.last_read_at DESC`

	rows, err := s.Pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, fmt.Errorf("read receipts: %w", err)
	}
	defer rows.Close()

	var out []models.ReadReceipt
	for rows.Next() {
		var r models.ReadReceipt
		if err := rows.Scan(&r.UserID, &r.Username, &r.LastReadAt); err != nil {
			return nil, fmt.Errorf("scan read receipt: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate read receipts: %w", err)
	}
	return out, nil
}

// UnreadCounts returns, for every room that has any, the number of messages
// authored by other users and created after the user's last-read cursor.
// Rooms without a read-state row count every other user's message (no cursor
// = nothing read yet). Rooms with nothing unread are omitted entirely.
func (s *Store) UnreadCounts(ctx context.Context, userID uuid.UUID) ([]models.RoomUnread, error) {
	const q = `
		SELECT m.room_id, count(*)
		FROM messages m
		LEFT JOIN message_read_state r
			ON r.room_id = m.room_id AND r.user_id = $1
		WHERE m.user_id <> $1
			AND m.created_at > COALESCE(r.last_read_at, '-infinity'::timestamptz)
		GROUP BY m.room_id`

	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("unread counts: %w", err)
	}
	defer rows.Close()

	var out []models.RoomUnread
	for rows.Next() {
		var u models.RoomUnread
		if err := rows.Scan(&u.RoomID, &u.UnreadCount); err != nil {
			return nil, fmt.Errorf("scan unread count: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unread counts: %w", err)
	}
	return out, nil
}
