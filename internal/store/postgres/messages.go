package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/models"
)

// CreateMessage inserts a message and returns the fully-populated row
// (including the denormalized sender username for broadcast).
func (s *Store) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, username, content string) (models.Message, error) {
	const q = `
		INSERT INTO messages (room_id, user_id, content)
		VALUES ($1, $2, $3)
		RETURNING id, room_id, user_id, $4::text AS username, content, created_at`

	m := models.Message{
		RoomID:   roomID,
		UserID:   userID,
		Username: username,
		Content:  content,
	}
	err := s.Pool.QueryRow(ctx, q, roomID, userID, content, username).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt)
	if err != nil {
		return models.Message{}, fmt.Errorf("create message: %w", err)
	}
	return m, nil
}

// ListMessages returns up to limit messages in a room created before the given
// cursor (exclusive), newest-first. Pass a zero-time cursor to fetch the most
// recent messages. limit is clamped to [1, 100].
func (s *Store) ListMessages(ctx context.Context, roomID uuid.UUID, before time.Time, limit int) ([]models.Message, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	const q = `
		SELECT m.id, m.room_id, m.user_id, u.username, m.content, m.created_at
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1 AND ($2::timestamptz IS NULL OR m.created_at < $2)
		ORDER BY m.created_at DESC
		LIMIT $3`

	rows, err := s.Pool.Query(ctx, q, roomID, nullableTime(before), limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	msgs := make([]models.Message, 0, limit)
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return msgs, nil
}

// nullableTime returns nil for the zero time so the SQL `IS NULL OR` branch
// matches "no cursor".
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
