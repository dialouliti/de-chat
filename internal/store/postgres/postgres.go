// Package postgres implements the persistence layer on top of pgx.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgsqlmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" as a database/sql driver

	"github.com/pavelananev/dechat/internal/store/migrations"
)

// Store wraps a pgx connection pool and exposes domain queries.
type Store struct {
	Pool *pgxpool.Pool
}

// New creates a connection pool against the given database URL and pings it.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{Pool: pool}, nil
}

// Close releases all pool connections.
func (s *Store) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}

// Migrate runs all embedded up migrations against the database. It is safe to
// call on every startup: already-applied migrations are skipped.
//
// golang-migrate needs a *sql.DB, so we open a short-lived connection via the
// pgx stdlib driver. The app's query path keeps using the pgxpool pool.
func (s *Store) Migrate(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer db.Close()

	// golang-migrate needs its own database.Driver, so wrap the *sql.DB with
	// the postgres driver's WithInstance.
	driver, err := pgsqlmigrate.WithInstance(db, &pgsqlmigrate.Config{})
	if err != nil {
		return fmt.Errorf("create migrate driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
