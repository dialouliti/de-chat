package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pavelananev/dechat/internal/models"
)

// ErrNotFound is returned when a single-row query matches nothing.
var ErrNotFound = errors.New("not found")

// CreateUser inserts a new user. Returns the created row.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, role models.Role) (models.User, error) {
	const q = `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, username, password_hash, role, disabled, created_at, updated_at`

	var u models.User
	err := s.Pool.QueryRow(ctx, q, username, passwordHash, string(role)).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return models.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// GetUserByUsername loads a user by username. Returns ErrNotFound if missing.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	const q = `
		SELECT id, username, password_hash, role, disabled, created_at, updated_at
		FROM users WHERE username = $1`

	var u models.User
	err := s.Pool.QueryRow(ctx, q, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.User{}, ErrNotFound
		}
		return models.User{}, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

// GetUserByID loads a user by id. Returns ErrNotFound if missing.
func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (models.User, error) {
	const q = `
		SELECT id, username, password_hash, role, disabled, created_at, updated_at
		FROM users WHERE id = $1`

	var u models.User
	err := s.Pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.User{}, ErrNotFound
		}
		return models.User{}, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// ListUsers returns all users ordered by username.
func (s *Store) ListUsers(ctx context.Context) ([]models.User, error) {
	const q = `
		SELECT id, username, password_hash, role, disabled, created_at, updated_at
		FROM users ORDER BY username`

	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

// HasAdmin reports whether at least one admin user exists. Used for the
// bootstrap-admin logic at startup.
func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM users WHERE role = 'admin')`
	var exists bool
	if err := s.Pool.QueryRow(ctx, q).Scan(&exists); err != nil {
		return false, fmt.Errorf("check admin exists: %w", err)
	}
	return exists, nil
}

// DeleteUser removes a user by id. Note: the messages.user_id foreign key is
// ON DELETE CASCADE, so this also removes that user's messages. "Keep
// everything forever" governs automatic retention only; admin deletions are
// explicit and destructive.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM users WHERE id = $1`
	ct, err := s.Pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserDisabled enables or disables a user. A disabled user cannot log in
// and is rejected on every authenticated request, including the WebSocket
// handshake, so blocking takes effect immediately even for users with a
// still-valid JWT. Returns ErrNotFound if the user does not exist.
func (s *Store) SetUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) error {
	const q = `UPDATE users SET disabled = $2, updated_at = now() WHERE id = $1`
	ct, err := s.Pool.Exec(ctx, q, id, disabled)
	if err != nil {
		return fmt.Errorf("set user disabled: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
