// Package config loads application configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration for the application.
type Config struct {
	ServerAddr    string
	DatabaseURL   string
	JWTSecret     string
	JWTTTL        time.Duration
	AdminUsername string
	AdminPassword string // may be empty -> a random one is generated at startup
	LogLevel      string
	// CORSOrigin controls the browser CORS policy. "*", a single origin, or a
	// comma-separated list. Empty defaults to "*". The web client needs this
	// when it is not served from the same origin as the API.
	CORSOrigin string
}

// Load reads configuration from the environment. Required values that are
// missing cause Load to return an error.
func Load() (Config, error) {
	cfg := Config{
		ServerAddr:    env("SERVER_ADDR", ":8080"),
		DatabaseURL:   env("DATABASE_URL", ""),
		JWTSecret:     env("JWT_SECRET", ""),
		JWTTTL:        24 * time.Hour,
		AdminUsername: env("ADMIN_USERNAME", "admin"),
		AdminPassword: env("ADMIN_PASSWORD", ""),
		LogLevel:      strings.ToLower(env("LOG_LEVEL", "info")),
		CORSOrigin:    env("CORS_ORIGIN", "*"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}

	ttl, err := time.ParseDuration(env("JWT_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid JWT_TTL: %w", err)
	}
	cfg.JWTTTL = ttl

	if cfg.AdminUsername == "" {
		return Config{}, fmt.Errorf("ADMIN_USERNAME must not be empty")
	}

	return cfg, nil
}

// env reads a single environment variable, returning fallback when unset.
func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
