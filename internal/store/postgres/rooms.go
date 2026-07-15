package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pavelananev/dechat/internal/models"
)

// CreateRoom inserts a new room and records the creator as its owner in
// room_members, in a single transaction. Returns the created room row.
func (s *Store) CreateRoom(ctx context.Context, name, description string, createdBy uuid.UUID) (models.Room, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.Room{}, fmt.Errorf("create room (begin): %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe to roll back after commit

	const insertRoom = `
		INSERT INTO rooms (name, description, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, name, description, created_by, created_at`

	var r models.Room
	err = tx.QueryRow(ctx, insertRoom, name, description, createdBy).
		Scan(&r.ID, &r.Name, &r.Description, &r.CreatedBy, &r.CreatedAt)
	if err != nil {
		return models.Room{}, fmt.Errorf("create room: %w", err)
	}

	// Record the creator as owner. ON CONFLICT keeps this idempotent if a row
	// somehow already exists.
	const insertOwner = `
		INSERT INTO room_members (room_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT (room_id, user_id) DO NOTHING`
	if _, err := tx.Exec(ctx, insertOwner, r.ID, createdBy); err != nil {
		return models.Room{}, fmt.Errorf("create room owner: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Room{}, fmt.Errorf("create room (commit): %w", err)
	}
	return r, nil
}

// GetRoomByID loads a room by id. Returns ErrNotFound if missing.
func (s *Store) GetRoomByID(ctx context.Context, id uuid.UUID) (models.Room, error) {
	const q = `
		SELECT id, name, description, created_by, created_at
		FROM rooms WHERE id = $1`

	var r models.Room
	err := s.Pool.QueryRow(ctx, q, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.CreatedBy, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Room{}, ErrNotFound
		}
		return models.Room{}, fmt.Errorf("get room by id: %w", err)
	}
	return r, nil
}

// ListRooms returns all rooms ordered by name.
func (s *Store) ListRooms(ctx context.Context) ([]models.Room, error) {
	const q = `
		SELECT id, name, description, created_by, created_at
		FROM rooms ORDER BY name`

	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	defer rows.Close()

	var rooms []models.Room
	for rows.Next() {
		var r models.Room
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan room: %w", err)
		}
		rooms = append(rooms, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rooms: %w", err)
	}
	return rooms, nil
}

// DeleteRoom removes a room. messages.room_id is ON DELETE CASCADE, so the
// room's messages are removed with it.
func (s *Store) DeleteRoom(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM rooms WHERE id = $1`
	ct, err := s.Pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete room: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearRoom deletes all messages in a room, leaving the room itself intact.
// This implements the admin "clear room" action.
func (s *Store) ClearRoom(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM messages WHERE room_id = $1`
	_, err := s.Pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("clear room: %w", err)
	}
	return nil
}
