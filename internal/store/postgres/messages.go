package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pavelananev/dechat/internal/models"
)

// ErrForbidden is returned when an operation is rejected because the caller
// lacks ownership/permission (e.g. editing another user's message).
var ErrForbidden = errors.New("forbidden")

// CreateMessage inserts a message and returns the fully-populated row
// (including the denormalized sender username for broadcast).
func (s *Store) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, username, content string) (models.Message, error) {
	const q = `
		INSERT INTO messages (room_id, user_id, content)
		VALUES ($1, $2, $3)
		RETURNING id, room_id, user_id, $4::text AS username, content, created_at, edited_at`

	m := models.Message{
		RoomID:   roomID,
		UserID:   userID,
		Username: username,
		Content:  content,
	}
	err := s.Pool.QueryRow(ctx, q, roomID, userID, content, username).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt)
	if err != nil {
		return models.Message{}, fmt.Errorf("create message: %w", err)
	}
	return m, nil
}

// UpdateMessage edits a message owned by userID. Only the author may edit;
// other callers get ErrForbidden, a missing message gets ErrNotFound.
// The denormalized username is passed back in via RETURNING to match
// CreateMessage's shape for broadcast.
func (s *Store) UpdateMessage(ctx context.Context, messageID, userID uuid.UUID, username, content string) (models.Message, error) {
	const q = `
		UPDATE messages SET content = $1, edited_at = now()
		WHERE id = $2 AND user_id = $3
		RETURNING id, room_id, user_id, $4::text AS username, content, created_at, edited_at`

	var m models.Message
	err := s.Pool.QueryRow(ctx, q, content, messageID, userID, username).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "doesn't exist" from "exists but not yours" so the
			// caller can return 404 vs 403. We probe to tell them apart.
			exists, lookupErr := s.messageExists(ctx, messageID)
			if lookupErr != nil || !exists {
				return models.Message{}, ErrNotFound
			}
			return models.Message{}, ErrForbidden
		}
		return models.Message{}, fmt.Errorf("update message: %w", err)
	}
	return m, nil
}

// messageExists reports whether a row with the given id is present.
func (s *Store) messageExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)`, id).Scan(&ok)
	if err != nil {
		return false, err
	}
	return ok, nil
}

// UpdateMessageAsAdmin edits a message regardless of authorship. Used by the
// global-admin edit path. Returns ErrNotFound if the message does not exist.
func (s *Store) UpdateMessageAsAdmin(ctx context.Context, messageID uuid.UUID, content string) (models.Message, error) {
	const q = `
		UPDATE messages SET content = $1, edited_at = now()
		WHERE id = $2
		RETURNING id, room_id, user_id,
			(SELECT u.username FROM users u WHERE u.id = messages.user_id) AS username,
			content, created_at, edited_at`

	var m models.Message
	err := s.Pool.QueryRow(ctx, q, content, messageID).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Message{}, ErrNotFound
		}
		return models.Message{}, fmt.Errorf("update message as admin: %w", err)
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
		SELECT m.id, m.room_id, m.user_id, u.username, m.content, m.created_at, m.edited_at
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
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt); err != nil {
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
