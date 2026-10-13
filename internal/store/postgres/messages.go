package postgres

import (
	"context"
	"encoding/json"
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

// ErrAlreadyEdited is returned when an edit is attempted on a message that
// has already been edited once. Messages may only be edited a single time.
var ErrAlreadyEdited = errors.New("message already edited")

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
// other callers get ErrForbidden, a missing message gets ErrNotFound, and a
// message that was already edited once gets ErrAlreadyEdited.
// The denormalized username is passed back in via RETURNING to match
// CreateMessage's shape for broadcast.
func (s *Store) UpdateMessage(ctx context.Context, messageID, userID uuid.UUID, username, content string) (models.Message, error) {
	const q = `
		UPDATE messages SET content = $1, edited_at = now()
		WHERE id = $2 AND user_id = $3 AND edited_at IS NULL
		RETURNING id, room_id, user_id, $4::text AS username, content, created_at, edited_at`

	var m models.Message
	err := s.Pool.QueryRow(ctx, q, content, messageID, userID, username).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The row didn't match: it may not exist, may not belong to the
			// caller, or may already have been edited. Probe to tell them apart.
			return models.Message{}, s.classifyEditFailure(ctx, messageID)
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

// classifyEditFailure disambiguates a 0-row edit result into ErrNotFound,
// ErrForbidden, or ErrAlreadyEdited. Shared by the author and admin paths so
// clients get an accurate reason regardless of the caller's permissions.
func (s *Store) classifyEditFailure(ctx context.Context, messageID uuid.UUID) error {
	var editedAt *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT edited_at FROM messages WHERE id = $1`, messageID).Scan(&editedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("classify edit failure: %w", err)
	}
	if editedAt != nil {
		return ErrAlreadyEdited
	}
	return ErrForbidden
}

// UpdateMessageAsAdmin edits a message regardless of authorship. Used by the
// global-admin edit path. Returns ErrNotFound if the message does not exist,
// or ErrAlreadyEdited if it has already been edited once.
func (s *Store) UpdateMessageAsAdmin(ctx context.Context, messageID uuid.UUID, content string) (models.Message, error) {
	const q = `
		UPDATE messages SET content = $1, edited_at = now()
		WHERE id = $2 AND edited_at IS NULL
		RETURNING id, room_id, user_id,
			(SELECT u.username FROM users u WHERE u.id = messages.user_id) AS username,
			content, created_at, edited_at`

	var m models.Message
	err := s.Pool.QueryRow(ctx, q, content, messageID).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Message{}, s.classifyEditFailure(ctx, messageID)
		}
		return models.Message{}, fmt.Errorf("update message as admin: %w", err)
	}
	return m, nil
}

// ListMessages returns up to limit messages in a room created before the given
// cursor (exclusive), newest-first. Pass a zero-time cursor to fetch the most
// recent messages. limit is clamped to [1, 100]. Each message carries its
// aggregated emoji reactions (m.Reactions), oldest reaction first.
func (s *Store) ListMessages(ctx context.Context, roomID uuid.UUID, before time.Time, limit int) ([]models.Message, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	const q = `
		SELECT m.id, m.room_id, m.user_id, u.username, m.content, m.created_at, m.edited_at,
			COALESCE(react.aggregated, '[]'::json)
		FROM messages m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN LATERAL (
			SELECT json_agg(json_build_object(
				'emoji', r.emoji,
				'user_id', r.user_id,
				'username', ru.username
			) ORDER BY r.created_at) AS aggregated
			FROM message_reactions r
			JOIN users ru ON ru.id = r.user_id
			WHERE r.message_id = m.id
		) react ON true
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
		var reactions []byte
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt, &reactions); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if err := json.Unmarshal(reactions, &m.Reactions); err != nil {
			return nil, fmt.Errorf("unmarshal reactions: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return msgs, nil
}

// ListMessagesAfter returns up to limit messages in a room created after the
// given cursor (exclusive), oldest-first — the ascending order clients need to
// append a backfill page after a reconnect. Pass a zero-time cursor to fetch
// from the beginning of the room. limit is clamped to [1, 100]. Each message
// carries its aggregated emoji reactions (m.Reactions), oldest reaction first.
func (s *Store) ListMessagesAfter(ctx context.Context, roomID uuid.UUID, after time.Time, limit int) ([]models.Message, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	const q = `
		SELECT m.id, m.room_id, m.user_id, u.username, m.content, m.created_at, m.edited_at,
			COALESCE(react.aggregated, '[]'::json)
		FROM messages m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN LATERAL (
			SELECT json_agg(json_build_object(
				'emoji', r.emoji,
				'user_id', r.user_id,
				'username', ru.username
			) ORDER BY r.created_at) AS aggregated
			FROM message_reactions r
			JOIN users ru ON ru.id = r.user_id
			WHERE r.message_id = m.id
		) react ON true
		WHERE m.room_id = $1 AND ($2::timestamptz IS NULL OR m.created_at > $2)
		ORDER BY m.created_at ASC
		LIMIT $3`

	rows, err := s.Pool.Query(ctx, q, roomID, nullableTime(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list messages after: %w", err)
	}
	defer rows.Close()

	msgs := make([]models.Message, 0, limit)
	for rows.Next() {
		var m models.Message
		var reactions []byte
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt, &m.EditedAt, &reactions); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if err := json.Unmarshal(reactions, &m.Reactions); err != nil {
			return nil, fmt.Errorf("unmarshal reactions: %w", err)
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
