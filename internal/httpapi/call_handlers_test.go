package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/config"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// newCallRouter builds a minimal router that mirrors how router.go mounts the
// call endpoints: same paths, inside a RequireAuth group. It exists so the
// auth behavior of the routes can be tested without a database.
func newCallRouter(authSvc *auth.Service, deps CallDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireAuth)
			r.Get("/call/config", CallDeps{ICE: deps.ICE}.CallConfig)
			r.Post("/rooms/{id}/call", deps.StartCall)
		})
	})
	return r
}

// getCallConfig performs one authenticated GET /api/v1/call/config and decodes
// the body. An empty token sends no Authorization header.
func getCallConfig(t *testing.T, srv *httptest.Server, token string) (int, callConfigResponse) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/call/config", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get /call/config: %v", err)
	}
	defer resp.Body.Close()

	var body callConfigResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode, body
}

// TestTurnCredentialsDeterminism pins the coturn REST API credential scheme:
// same secret/username/expiry must produce the same base64 HMAC-SHA1 (28
// chars for the 20-byte digest), and any input change must change it.
func TestTurnCredentialsDeterminism(t *testing.T) {
	const (
		secret  = "s3cret-shared-turn-key"
		subject = "alice"
	)
	expiry := time.Unix(1770000000, 0).UTC()

	username1, cred1 := turnCredentials(secret, subject, expiry)
	username2, cred2 := turnCredentials(secret, subject, expiry)
	if username1 != username2 || cred1 != cred2 {
		t.Fatalf("credentials not deterministic: (%q, %q) vs (%q, %q)", username1, cred1, username2, cred2)
	}

	// Username is "<unix-expiry>:<username>".
	if want := "1770000000:alice"; username1 != want {
		t.Fatalf("username = %q, want %q", username1, want)
	}

	// base64.StdEncoding of a 20-byte SHA-1 digest is exactly 28 chars.
	if len(cred1) != 28 {
		t.Fatalf("credential length = %d, want 28 (base64 of a 20-byte HMAC-SHA1)", len(cred1))
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(cred1)
	if err != nil {
		t.Fatalf("credential is not valid standard base64: %v", err)
	}
	if len(raw) != sha1.Size {
		t.Fatalf("decoded credential = %d bytes, want %d", len(raw), sha1.Size)
	}

	// Known-answer vector: recomputed with the same stdlib primitives, it
	// guards the scheme against accidental drift (wrong hash, wrong encoding,
	// swapped key/message).
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username1))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); cred1 != want {
		t.Fatalf("credential = %q, want %q", cred1, want)
	}

	// Different expiry, subject, or secret must yield a different credential.
	if _, c := turnCredentials(secret, subject, expiry.Add(time.Second)); c == cred1 {
		t.Fatal("credential did not change with expiry")
	}
	if _, c := turnCredentials(secret, "bob", expiry); c == cred1 {
		t.Fatal("credential did not change with username")
	}
	if _, c := turnCredentials("another-key", subject, expiry); c == cred1 {
		t.Fatal("credential did not change with secret")
	}
}

// TestCallConfigSTUNOnly checks the default shape: STUN is always served and
// no TURN entry appears when the server has no TURN configuration.
func TestCallConfigSTUNOnly(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	stun := []string{"stun:stun.example.com:3478"}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{ICE: config.ICEConfig{
		STUNURLs: stun,
		TURNTTL:  time.Hour, // same default as config.Load
	}}))
	defer srv.Close()

	status, body := getCallConfig(t, srv, token)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if body.TTLSeconds != 3600 {
		t.Fatalf("ttl_seconds = %d, want 3600", body.TTLSeconds)
	}
	if len(body.ICEServers) != 1 {
		t.Fatalf("ice_servers has %d entries, want 1 (STUN only)", len(body.ICEServers))
	}
	got := body.ICEServers[0]
	if strings.Join(got.URLs, ",") != strings.Join(stun, ",") {
		t.Fatalf("STUN urls = %v, want %v", got.URLs, stun)
	}
	if got.Username != "" || got.Credential != "" {
		t.Fatalf("STUN entry must not carry credentials, got username=%q credential=%q", got.Username, got.Credential)
	}
}

// TestCallConfigWithTURN checks the TURN entry: URL passthrough, username in
// the "<unix-expiry>:<caller>" shape with an expiry in the future, and a
// credential that verifies against the shared secret.
func TestCallConfigWithTURN(t *testing.T) {
	const turnSecret = "shared-turn-secret"
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	stun := []string{"stun:stun.example.com:3478"}
	turn := []string{
		"turn:turn.example.com:3478?transport=udp",
		"turn:turn.example.com:3478?transport=tcp",
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{ICE: config.ICEConfig{
		STUNURLs:   stun,
		TURNURLs:   turn,
		TURNSecret: turnSecret,
		TURNTTL:    time.Hour,
	}}))
	defer srv.Close()

	before := time.Now().Unix()
	status, body := getCallConfig(t, srv, token)
	after := time.Now().Unix()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	if len(body.ICEServers) != 2 {
		t.Fatalf("ice_servers has %d entries, want 2 (STUN + TURN)", len(body.ICEServers))
	}
	if strings.Join(body.ICEServers[0].URLs, ",") != strings.Join(stun, ",") {
		t.Fatalf("first entry urls = %v, want the STUN list %v", body.ICEServers[0].URLs, stun)
	}

	turnEntry := body.ICEServers[1]
	if strings.Join(turnEntry.URLs, ",") != strings.Join(turn, ",") {
		t.Fatalf("TURN urls = %v, want %v", turnEntry.URLs, turn)
	}

	// username must be "<unix-expiry>:alice" with the expiry at now+TTL.
	parts := strings.SplitN(turnEntry.Username, ":", 2)
	if len(parts) != 2 || parts[1] != "alice" {
		t.Fatalf("TURN username = %q, want \"<unix-expiry>:alice\"", turnEntry.Username)
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("TURN username expiry %q is not a unix timestamp: %v", parts[0], err)
	}
	// The credential is generated inside [before, after] + TTL; allow a
	// little slack for slow clocks.
	if expiry < before+3595 || expiry > after+3605 {
		t.Fatalf("TURN username expiry = %d, want ~%d..%d (now+1h)", expiry, before+3595, after+3605)
	}

	// The credential must verify: base64(HMAC-SHA1(secret, username)).
	mac := hmac.New(sha1.New, []byte(turnSecret))
	mac.Write([]byte(turnEntry.Username))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); turnEntry.Credential != want {
		t.Fatalf("TURN credential = %q, want %q", turnEntry.Credential, want)
	}
}

// TestCallConfigUnauthorized checks the RequireAuth gate on the route.
func TestCallConfigUnauthorized(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{ICE: config.ICEConfig{
		STUNURLs: []string{"stun:stun.example.com:3478"},
		TURNTTL:  time.Hour,
	}}))
	defer srv.Close()

	if status, _ := getCallConfig(t, srv, ""); status != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want %d", status, http.StatusUnauthorized)
	}
	if status, _ := getCallConfig(t, srv, "not-a-jwt"); status != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d, want %d", status, http.StatusUnauthorized)
	}
}

// --- POST /rooms/{id}/call -------------------------------------------------

// fakeRoomGetter implements RoomGetter for the StartCall tests: it knows
// exactly one room and answers postgres.ErrNotFound for anything else. With
// fail set, every lookup fails with a plain error (the 500 path).
type fakeRoomGetter struct {
	room models.Room
	fail bool
}

var _ RoomGetter = (*fakeRoomGetter)(nil)

func (f *fakeRoomGetter) GetRoomByID(ctx context.Context, id uuid.UUID) (models.Room, error) {
	if f.fail {
		return models.Room{}, errors.New("fakeRoomGetter: simulated store failure")
	}
	if id == f.room.ID {
		return f.room, nil
	}
	return models.Room{}, postgres.ErrNotFound
}

// startCall performs one authenticated POST /api/v1/rooms/{roomID}/call and
// decodes the body. An empty token sends no Authorization header.
func startCall(t *testing.T, srv *httptest.Server, token, roomID string) (int, callTokenResponse) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/rooms/"+roomID+"/call", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post /rooms/%s/call: %v", roomID, err)
	}
	defer resp.Body.Close()

	var body callTokenResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode, body
}

// livekitTestPair mirrors the compose dev defaults for the SFU API pair.
var livekitTestPair = config.LiveKitConfig{
	URL:       "ws://livekit.example.com:7880",
	APIKey:    "devkey",
	APISecret: "livekit-test-secret",
}

// TestStartCallUnauthorized checks the RequireAuth gate on the route.
func TestStartCallUnauthorized(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{LiveKit: livekitTestPair}))
	defer srv.Close()

	roomID := uuid.New().String()
	if status, _ := startCall(t, srv, "", roomID); status != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want %d", status, http.StatusUnauthorized)
	}
	if status, _ := startCall(t, srv, "not-a-jwt", roomID); status != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d, want %d", status, http.StatusUnauthorized)
	}
}

// TestStartCallInvalidRoomID checks the 400 on a non-UUID room id.
func TestStartCallInvalidRoomID(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{LiveKit: livekitTestPair}))
	defer srv.Close()

	if status, _ := startCall(t, srv, token, "not-a-uuid"); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
}

// TestStartCallSFUDisabled checks the 503 when the server has no LiveKit API
// pair. The store is nil on purpose: the disabled check runs before it, and
// a nil interface would panic (failing the test) if that order ever broke.
func TestStartCallSFUDisabled(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{
		LiveKit: config.LiveKitConfig{URL: "ws://livekit.example.com:7880"}, // no API pair
	}))
	defer srv.Close()

	if status, _ := startCall(t, srv, token, uuid.New().String()); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

// TestStartCallRoomNotFound checks the 404 for a room the store doesn't know.
func TestStartCallRoomNotFound(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{
		Store:   &fakeRoomGetter{room: models.Room{ID: uuid.New()}},
		LiveKit: livekitTestPair,
	}))
	defer srv.Close()

	if status, _ := startCall(t, srv, token, uuid.New().String()); status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
}

// TestStartCallStoreError checks that a failing store surfaces as a 500, not
// as a token.
func TestStartCallStoreError(t *testing.T) {
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(uuid.New(), "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{
		Store:   &fakeRoomGetter{fail: true},
		LiveKit: livekitTestPair,
	}))
	defer srv.Close()

	if status, _ := startCall(t, srv, token, uuid.New().String()); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
}

// TestStartCallOK checks the happy path: the configured SFU URL is passed
// through and the token is a valid LiveKit access JWT for the caller and the
// room — verified by parsing it with the API secret, exactly like the SFU
// would.
func TestStartCallOK(t *testing.T) {
	userID := uuid.New()
	room := models.Room{ID: uuid.New(), Name: "general"}
	authSvc := auth.New("test-jwt-secret-0123456789abcdef", time.Hour)
	token, err := authSvc.IssueToken(userID, "alice", "user")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	srv := httptest.NewServer(newCallRouter(authSvc, CallDeps{
		Store:   &fakeRoomGetter{room: room},
		LiveKit: livekitTestPair,
	}))
	defer srv.Close()

	before := time.Now()
	status, body := startCall(t, srv, token, room.ID.String())
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	if body.URL != livekitTestPair.URL {
		t.Fatalf("url = %q, want %q", body.URL, livekitTestPair.URL)
	}
	if body.ExpiresAt.Before(before) {
		t.Fatalf("expires_at = %v, must be in the future", body.ExpiresAt)
	}

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(body.Token, claims, func(t *jwt.Token) (any, error) {
		return []byte(livekitTestPair.APISecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		t.Fatalf("parse call token: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("call token is not valid")
	}
	if got, _ := claims.GetSubject(); got != userID.String() {
		t.Fatalf("sub = %q, want the caller's user id %q", got, userID.String())
	}
	if got, _ := claims.GetIssuer(); got != livekitTestPair.APIKey {
		t.Fatalf("iss = %q, want %q", got, livekitTestPair.APIKey)
	}
	if got, _ := claims["name"]; got != "alice" {
		t.Fatalf("name = %#v, want %q", got, "alice")
	}
	video, ok := claims["video"].(map[string]any)
	if !ok {
		t.Fatalf("video claim = %#v, want a grant object", claims["video"])
	}
	if got, _ := video["room"].(string); got != room.ID.String() {
		t.Fatalf("video.room = %q, want the room id %q", got, room.ID.String())
	}
	if got, _ := video["roomJoin"].(bool); !got {
		t.Fatalf("video.roomJoin = %#v, want true", video["roomJoin"])
	}
}
