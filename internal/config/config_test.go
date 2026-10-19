package config

import (
	"testing"
	"time"
)

// baseEnv sets the variables every Load call needs to get past the required
// checks. Individual tests set the ICE knobs they exercise on top. The
// TURN_* variables are not reset here: setting them to "" would change
// TURN_TTL parsing ("" is not a valid duration), so tests that assert
// defaults must set the relevant knobs explicitly.
func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
}

func TestLoadICEDefaults(t *testing.T) {
	baseEnv(t)
	// Empty behaves as unset for these two (an empty TURN_URLS list means
	// "no TURN" either way).
	t.Setenv("TURN_URLS", "")
	t.Setenv("TURN_SECRET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []string{"stun:stun.l.google.com:19302"}; len(cfg.ICE.STUNURLs) != 1 || cfg.ICE.STUNURLs[0] != want[0] {
		t.Fatalf("STUNURLs = %v, want %v", cfg.ICE.STUNURLs, want)
	}
	if len(cfg.ICE.TURNURLs) != 0 {
		t.Fatalf("TURNURLs = %v, want empty", cfg.ICE.TURNURLs)
	}
	if cfg.ICE.TURNSecret != "" {
		t.Fatalf("TURNSecret = %q, want empty", cfg.ICE.TURNSecret)
	}
	if cfg.ICE.TURNTTL != time.Hour {
		t.Fatalf("TURNTTL = %s, want 1h", cfg.ICE.TURNTTL)
	}
}

func TestLoadSTUNList(t *testing.T) {
	baseEnv(t)
	t.Setenv("STUN_URLS", "stun:a.example.com:3478, stun:b.example.com:3478")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"stun:a.example.com:3478", "stun:b.example.com:3478"}
	if len(cfg.ICE.STUNURLs) != len(want) || cfg.ICE.STUNURLs[0] != want[0] || cfg.ICE.STUNURLs[1] != want[1] {
		t.Fatalf("STUNURLs = %v, want %v (entries must be trimmed)", cfg.ICE.STUNURLs, want)
	}
}

func TestLoadSTUNEmptyFallsBackToDefault(t *testing.T) {
	baseEnv(t)
	t.Setenv("STUN_URLS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "stun:stun.l.google.com:19302"; len(cfg.ICE.STUNURLs) != 1 || cfg.ICE.STUNURLs[0] != want {
		t.Fatalf("STUNURLs = %v, want [%q]: STUN must always be served", cfg.ICE.STUNURLs, want)
	}
}

func TestLoadTURNRequiresSecret(t *testing.T) {
	baseEnv(t)
	t.Setenv("TURN_URLS", "turn:turn.example.com:3478")
	t.Setenv("TURN_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load must fail when TURN_URLS is set but TURN_SECRET is empty")
	}
}

func TestLoadTURNConfigured(t *testing.T) {
	baseEnv(t)
	t.Setenv("TURN_URLS", "turn:turn.example.com:3478?transport=udp,turn:turn2.example.com:3478")
	t.Setenv("TURN_SECRET", "shared-secret")
	t.Setenv("TURN_TTL", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"turn:turn.example.com:3478?transport=udp", "turn:turn2.example.com:3478"}
	if len(cfg.ICE.TURNURLs) != len(want) || cfg.ICE.TURNURLs[0] != want[0] || cfg.ICE.TURNURLs[1] != want[1] {
		t.Fatalf("TURNURLs = %v, want %v", cfg.ICE.TURNURLs, want)
	}
	if cfg.ICE.TURNSecret != "shared-secret" {
		t.Fatalf("TURNSecret = %q, want %q", cfg.ICE.TURNSecret, "shared-secret")
	}
	if cfg.ICE.TURNTTL != 30*time.Minute {
		t.Fatalf("TURNTTL = %s, want 30m", cfg.ICE.TURNTTL)
	}
}

func TestLoadInvalidTURNTTL(t *testing.T) {
	baseEnv(t)
	t.Setenv("TURN_TTL", "not-a-duration")

	if _, err := Load(); err == nil {
		t.Fatal("Load must fail on an unparseable TURN_TTL")
	}
}

func TestLoadNonPositiveTURNTTLWithTURN(t *testing.T) {
	baseEnv(t)
	t.Setenv("TURN_URLS", "turn:turn.example.com:3478")
	t.Setenv("TURN_SECRET", "shared-secret")
	t.Setenv("TURN_TTL", "0s")

	if _, err := Load(); err == nil {
		t.Fatal("Load must fail when TURN is configured with a non-positive TURN_TTL")
	}
}

func TestLoadLiveKitDefault(t *testing.T) {
	baseEnv(t)
	// Empty behaves as unset: the pair check sees two empty values, which
	// means the SFU feature is disabled. Set explicitly so a leaked host
	// env var can't flip the outcome.
	t.Setenv("LIVEKIT_API_KEY", "")
	t.Setenv("LIVEKIT_API_SECRET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LiveKit.APIKey != "" || cfg.LiveKit.APISecret != "" {
		t.Fatalf("LiveKit = %+v, want the zero config (SFU disabled)", cfg.LiveKit)
	}
}

func TestLoadLiveKitConfigured(t *testing.T) {
	baseEnv(t)
	t.Setenv("LIVEKIT_API_KEY", "devkey")
	t.Setenv("LIVEKIT_API_SECRET", "livekit-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LiveKit.APIKey != "devkey" || cfg.LiveKit.APISecret != "livekit-secret" {
		t.Fatalf("LiveKit = %+v, want {APIKey: devkey, APISecret: livekit-secret}", cfg.LiveKit)
	}
}

func TestLoadLiveKitKeyWithoutSecret(t *testing.T) {
	baseEnv(t)
	t.Setenv("LIVEKIT_API_KEY", "devkey")
	t.Setenv("LIVEKIT_API_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load must fail when LIVEKIT_API_KEY is set but LIVEKIT_API_SECRET is empty")
	}
}

func TestLoadLiveKitSecretWithoutKey(t *testing.T) {
	baseEnv(t)
	t.Setenv("LIVEKIT_API_KEY", "")
	t.Setenv("LIVEKIT_API_SECRET", "livekit-secret")

	if _, err := Load(); err == nil {
		t.Fatal("Load must fail when LIVEKIT_API_SECRET is set but LIVEKIT_API_KEY is empty")
	}
}
