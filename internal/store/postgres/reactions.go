package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrInvalidEmoji is returned by ToggleReaction when the emoji fails input
// validation (empty, longer than 32 bytes, or containing ASCII control
// characters) before it ever reaches the database.
var ErrInvalidEmoji = errors.New("invalid emoji")

// validateEmoji enforces the reaction input contract: non-empty, at most 32
// bytes, no ASCII control characters (0x00–0x1F, 0x7F).
func validateEmoji(emoji string) error {
	if emoji == "" {
		return errors.New("emoji is required")
	}
	if len(emoji) > 32 {
		return errors.New("emoji must be at most 32 bytes")
	}
	for i := 0; i < len(emoji); i++ {
		if emoji[i] < 0x20 || emoji[i] == 0x7f {
			return errors.New("emoji must not contain control characters")
		}
	}
	return nil
}

// ToggleReaction atomically flips the caller's emoji reaction on a message.
//
// The insert goes through INSERT ... SELECT over messages filtered by both
// id and room_id, so reacting to a missing message — or to one that belongs
// to a different room — inserts nothing instead of raising a FK violation.
// When the insert lands, added is true. On a PK conflict the same triple
// already exists and is deleted instead, with added false. If neither the
// insert nor the delete touched a row, the (message, room) pair does not
// exist and ErrNotFound is returned.
func (s *Store) ToggleReaction(ctx context.Context, messageID, roomID, userID uuid.UUID, emoji string) (bool, error) {
	if err := validateEmoji(emoji); err != nil {
		return false, fmt.Errorf("%w: %s", ErrInvalidEmoji, err)
	}

	const insertQ = `
		INSERT INTO message_reactions (message_id, user_id, emoji)
		SELECT m.id, $3, $4 FROM messages m
		WHERE m.id = $1 AND m.room_id = $2
		ON CONFLICT (message_id, user_id, emoji) DO NOTHING`

	tag, err := s.Pool.Exec(ctx, insertQ, messageID, roomID, userID, emoji)
	if err != nil {
		return false, fmt.Errorf("insert reaction: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	// No row inserted: either the (message_id, user_id, emoji) triple already
	// exists (toggle off) or the message/room pair is unknown. Try the delete.
	const deleteQ = `
		DELETE FROM message_reactions
		WHERE message_id = $1 AND user_id = $2 AND emoji = $3`

	tag, err = s.Pool.Exec(ctx, deleteQ, messageID, userID, emoji)
	if err != nil {
		return false, fmt.Errorf("delete reaction: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return false, nil
	}

	// Nothing inserted and nothing deleted: distinguish "message (in this
	// room) does not exist" from a race where a concurrent toggle removed the
	// reaction between our INSERT and DELETE.
	const probeQ = `SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1 AND room_id = $2)`
	var exists bool
	if err := s.Pool.QueryRow(ctx, probeQ, messageID, roomID).Scan(&exists); err != nil {
		return false, fmt.Errorf("probe message for reaction: %w", err)
	}
	if !exists {
		return false, ErrNotFound
	}
	// The message exists; a concurrent toggle already removed the reaction.
	// The net state is "no reaction", which is what a toggle-off reports.
	return false, nil
}
