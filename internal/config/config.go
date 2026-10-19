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
	// ICE carries the WebRTC STUN/TURN settings served to clients via
	// GET /api/v1/call/config.
	ICE ICEConfig
	// LiveKit carries the LiveKit SFU API credentials; an empty pair means
	// the SFU feature is disabled.
	LiveKit LiveKitConfig
}

// ICEConfig holds the WebRTC ICE (STUN/TURN) server settings. STUN is always
// served; TURN entries are added only when TURNURLs is non-empty, using
// coturn's REST API short-lived credential scheme — only the shared secret
// lives here, per-user credentials are derived per request by the handler.
type ICEConfig struct {
	STUNURLs   []string
	TURNURLs   []string      // empty -> TURN is disabled
	TURNSecret string        // required when TURNURLs is non-empty
	TURNTTL    time.Duration // lifetime of generated TURN credentials
}

// LiveKitConfig holds the API key/secret pair of the LiveKit SFU running in
// docker-compose.yml. The backend needs the secret to mint short-lived
// LiveKit access tokens for group calls (the token endpoint is a follow-up
// card); an empty pair means the SFU feature is disabled.
type LiveKitConfig struct {
	APIKey    string
	APISecret string
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
	// The HMAC signing key must be strong enough to resist brute force. A
	// short or default placeholder secret would let anyone forge admin JWTs,
	// so refuse to boot rather than silently running insecure.
	const minSecretLen = 32
	if len(cfg.JWTSecret) < minSecretLen {
		return Config{}, fmt.Errorf(
			"JWT_SECRET must be at least %d bytes long (got %d); generate one with `openssl rand -base64 32`",
			minSecretLen, len(cfg.JWTSecret),
		)
	}
	if isKnownWeakSecret(cfg.JWTSecret) {
		return Config{}, fmt.Errorf(
			"JWT_SECRET is a known default/placeholder value; set a strong random secret (e.g. `openssl rand -base64 32`)",
		)
	}

	ttl, err := time.ParseDuration(env("JWT_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid JWT_TTL: %w", err)
	}
	cfg.JWTTTL = ttl

	// --- WebRTC ICE (STUN/TURN) -------------------------------------------
	stunURLs := splitCSV(env("STUN_URLS", defaultSTUNURLs))
	if len(stunURLs) == 0 {
		// STUN is always served; an explicitly empty list would break every
		// call, so fall back to the default instead.
		stunURLs = []string{defaultSTUNURLs}
	}
	turnTTL, err := time.ParseDuration(env("TURN_TTL", defaultTURNTTL))
	if err != nil {
		return Config{}, fmt.Errorf("invalid TURN_TTL: %w", err)
	}
	turnSecret := env("TURN_SECRET", "")
	turnURLs := splitCSV(env("TURN_URLS", ""))
	if len(turnURLs) > 0 {
		// TURN credentials are HMAC-signed with the shared secret, so a
		// configured TURN server without a secret would hand out
		// credentials coturn rejects; refuse to boot instead.
		if turnSecret == "" {
			return Config{}, fmt.Errorf("TURN_SECRET is required when TURN_URLS is set")
		}
		if turnTTL <= 0 {
			return Config{}, fmt.Errorf("TURN_TTL must be positive when TURN_URLS is set (got %s)", turnTTL)
		}
	}
	cfg.ICE = ICEConfig{
		STUNURLs:   stunURLs,
		TURNURLs:   turnURLs,
		TURNSecret: turnSecret,
		TURNTTL:    turnTTL,
	}

	// --- LiveKit SFU --------------------------------------------------------
	// The API pair is all-or-nothing: with only one half set, tokens minted
	// for the SFU would be signed against an empty value (or validated
	// against an empty key) and rejected — refuse to boot so the
	// misconfiguration is caught early.
	livekitKey := env("LIVEKIT_API_KEY", "")
	livekitSecret := env("LIVEKIT_API_SECRET", "")
	if (livekitKey == "") != (livekitSecret == "") {
		return Config{}, fmt.Errorf(
			"LIVEKIT_API_KEY and LIVEKIT_API_SECRET must be set together: set both to enable the LiveKit SFU, or neither to disable it",
		)
	}
	cfg.LiveKit = LiveKitConfig{APIKey: livekitKey, APISecret: livekitSecret}

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

// Defaults for the WebRTC ICE settings.
const (
	defaultSTUNURLs = "stun:stun.l.google.com:19302"
	defaultTURNTTL  = "1h"
)

// splitCSV splits a comma-separated env value into trimmed, non-empty items.
// Whitespace around entries is tolerated ("a, b" -> ["a", "b"]); an empty
// value yields a nil slice.
func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// knownWeakSecrets are default/placeholder values shipped in the example and
// compose files. They are long enough to pass the length check but publicly
// known, so they must never be used for real signing.
var knownWeakSecrets = map[string]struct{}{
	"change-me-to-a-long-random-string": {},
}

// isKnownWeakSecret reports whether the secret is a documented default.
func isKnownWeakSecret(secret string) bool {
	_, ok := knownWeakSecrets[strings.TrimSpace(secret)]
	return ok
}
