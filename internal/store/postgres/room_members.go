package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pavelananev/dechat/internal/models"
)

// AddRoomMember adds a user to a room with a given role. If the membership row
// already exists, the role is updated (so this is an upsert).
func (s *Store) AddRoomMember(ctx context.Context, roomID, userID uuid.UUID, role models.RoomRole) error {
	const q = `
		INSERT INTO room_members (room_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (room_id, user_id) DO UPDATE SET role = EXCLUDED.role`
	_, err := s.Pool.Exec(ctx, q, roomID, userID, string(role))
	if err != nil {
		return fmt.Errorf("add room member: %w", err)
	}
	return nil
}

// RemoveRoomMember removes a membership row. Returns ErrNotFound if the row
// does not exist.
func (s *Store) RemoveRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`
	ct, err := s.Pool.Exec(ctx, q, roomID, userID)
	if err != nil {
		return fmt.Errorf("remove room member: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRoomMembers returns every member of a room with their role and username.
func (s *Store) ListRoomMembers(ctx context.Context, roomID uuid.UUID) ([]models.RoomMember, error) {
	const q = `
		SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1
		ORDER BY rm.role DESC, rm.joined_at`

	rows, err := s.Pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, fmt.Errorf("list room members: %w", err)
	}
	defer rows.Close()

	var out []models.RoomMember
	for rows.Next() {
		var m models.RoomMember
		if err := rows.Scan(&m.RoomID, &m.UserID, &m.Username, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan room member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room members: %w", err)
	}
	return out, nil
}

// GetRoomRole returns the caller's role in a room. Returns ("", ErrNotFound)
// if the user is not a member of the room.
func (s *Store) GetRoomRole(ctx context.Context, roomID, userID uuid.UUID) (models.RoomRole, error) {
	const q = `SELECT role FROM room_members WHERE room_id = $1 AND user_id = $2`
	var role string
	err := s.Pool.QueryRow(ctx, q, roomID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get room role: %w", err)
	}
	return models.RoomRole(role), nil
}
