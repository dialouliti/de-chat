// Command dechat runs the chat backend: HTTP API + WebSocket real-time layer.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/config"
	"github.com/pavelananev/dechat/internal/httpapi"
	"github.com/pavelananev/dechat/internal/hub"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/server"
	"github.com/pavelananev/dechat/internal/store/postgres"
	"github.com/pavelananev/dechat/internal/ws"

	"github.com/google/uuid"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid config", "error", err)
		os.Exit(1)
	}

	log := newLogger(cfg.LogLevel)

	// --- Database ---
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := postgres.New(ctx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		log.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		log.Error("run migrations", "error", err)
		os.Exit(1)
	}
	log.Info("database migrated")

	// --- Bootstrap admin ---
	if err := ensureBootstrapAdmin(context.Background(), store, cfg, log); err != nil {
		log.Error("bootstrap admin", "error", err)
		os.Exit(1)
	}

	// --- Services ---
	authSvc := auth.New(cfg.JWTSecret, cfg.JWTTTL)
	// Wire the store-backed disabled check so blocked users are rejected on
	// every request and on the WebSocket handshake.
	authSvc.SetUserChecker(func(ctx context.Context, userID uuid.UUID) (bool, error) {
		u, err := store.GetUserByID(ctx, userID)
		if err != nil {
			// Treat "not found" as disabled: a deleted user must not keep
			// accessing the API on an old token.
			return true, nil
		}
		return u.Disabled, nil
	})
	h := hub.New(log.With("component", "hub"))
	go h.Run(notifyOnStop())

	wsHandler := ws.New(h, store, authSvc, log.With("component", "ws"))
	handler := httpapi.New(httpapi.Deps{
		Store:      store,
		Auth:       authSvc,
		WS:         wsHandler,
		CORSOrigin: cfg.CORSOrigin,
		ICE:        cfg.ICE,
	})

	srv := server.New(cfg.ServerAddr, handler, log.With("component", "server"))

	// --- Lifecycle ---
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		// Trigger hub shutdown and HTTP shutdown together.
		shutdownCtx, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Start(); err != nil {
		log.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// newLogger builds a structured JSON logger at the requested level.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// ensureBootstrapAdmin creates the first admin if none exists. If
// ADMIN_PASSWORD is empty a random one is generated and logged once.
func ensureBootstrapAdmin(ctx context.Context, store *postgres.Store, cfg config.Config, log *slog.Logger) error {
	hasAdmin, err := store.HasAdmin(ctx)
	if err != nil {
		return err
	}
	if hasAdmin {
		log.Debug("admin already exists; skipping bootstrap")
		return nil
	}

	password := cfg.AdminPassword
	generated := false
	if password == "" {
		var err error
		password, err = randomPassword(20)
		if err != nil {
			return err
		}
		generated = true
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := store.CreateUser(ctx, cfg.AdminUsername, hash, models.RoleAdmin); err != nil {
		return err
	}

	if generated {
		log.Info("bootstrap admin created with generated password (save it now; it will not be shown again)",
			"username", cfg.AdminUsername, "password", password)
	} else {
		log.Info("bootstrap admin created", "username", cfg.AdminUsername)
	}
	return nil
}

// randomPassword returns a URL-safe random string of the given byte length.
func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// notifyOnStop returns a channel that closes on SIGINT/SIGTERM, which the hub
// uses to end its run loop.
func notifyOnStop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		close(done)
	}()
	return done
}
