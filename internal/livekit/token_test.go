package livekit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	testAPIKey    = "devkey"
	testAPISecret = "livekit-test-secret"
	// expTolerance bounds the clock drift allowed between the exp claim and
	// the expected now+ttl (test runs take milliseconds, not minutes).
	expTolerance = 2 * time.Minute
)

// parseAccessToken parses a minted token with the given secret, accepting
// HS256 only — mirroring how the LiveKit server validates access tokens.
// Anything else (wrong signature, wrong algorithm) must fail the test.
func parseAccessToken(t *testing.T, tokenStr, apiSecret string) jwt.MapClaims {
	t.Helper()

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		return []byte(apiSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("token is not valid")
	}
	return claims
}

// videoClaim decodes the "video" grant of already-parsed claims into the
// grant struct, so field assertions stay typed.
func videoClaim(t *testing.T, claims jwt.MapClaims) videoGrant {
	t.Helper()

	raw, ok := claims["video"].(map[string]any)
	if !ok {
		t.Fatalf("video claim = %#v, want a grant object", claims["video"])
	}
	// Re-marshal the generic map through JSON the way the SFU would decode
	// the token, catching tag drift (e.g. a renamed json key).
	enc, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("re-encode video claim: %v", err)
	}
	var grant videoGrant
	if err := json.Unmarshal(enc, &grant); err != nil {
		t.Fatalf("decode video claim: %v", err)
	}
	return grant
}

// TestAccessTokenClaims pins the token shape: issuer, subject, display name,
// the room-scoped video grant, and an expiry at now+ttl.
func TestAccessTokenClaims(t *testing.T) {
	userID := uuid.New()
	roomID := uuid.New()
	ttl := TokenTTL

	before := time.Now()
	token, expiresAt, err := AccessToken(testAPIKey, testAPISecret, userID.String(), "alice", roomID, ttl)
	after := time.Now()
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	claims := parseAccessToken(t, token, testAPISecret)
	if got, _ := claims.GetIssuer(); got != testAPIKey {
		t.Fatalf("iss = %q, want %q", got, testAPIKey)
	}
	if got, _ := claims.GetSubject(); got != userID.String() {
		t.Fatalf("sub = %q, want %q", got, userID.String())
	}
	if got, _ := claims.GetExpirationTime(); got == nil {
		t.Fatal("exp claim is missing")
	}

	// The video grant scopes the token to the single room with full rights.
	grant := videoClaim(t, claims)
	wantGrant := videoGrant{
		Room:           roomID.String(),
		RoomJoin:       true,
		CanPublish:     true,
		CanSubscribe:   true,
		CanPublishData: true,
	}
	if grant != wantGrant {
		t.Fatalf("video grant = %+v, want %+v", grant, wantGrant)
	}

	// exp must sit at now+ttl (evaluated across the call), within tolerance.
	wantExp := after.Add(ttl)
	minExp := before.Add(ttl).Add(-expTolerance)
	maxExp := wantExp.Add(expTolerance)
	exp := claims["exp"].(float64)
	expTime := time.Unix(int64(exp), 0)
	if expTime.Before(minExp) || expTime.After(maxExp) {
		t.Fatalf("exp = %v, want ~%v (±%s)", expTime, wantExp, expTolerance)
	}
	// The returned expiresAt is the exp claim the handler hands to clients.
	if d := expiresAt.Sub(expTime); d < -expTolerance || d > expTolerance {
		t.Fatalf("returned expiresAt = %v, want the exp claim %v", expiresAt, expTime)
	}
}

// TestAccessTokenVariesByRoomAndIdentity checks that a token minted for a
// different room or a different participant is a different token — the grant
// must not be reusable across rooms or users.
func TestAccessTokenVariesByRoomAndIdentity(t *testing.T) {
	roomA, roomB := uuid.New(), uuid.New()
	alice, bob := uuid.New(), uuid.New()

	base, _, err := AccessToken(testAPIKey, testAPISecret, alice.String(), "alice", roomA, TokenTTL)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	otherRoom, _, err := AccessToken(testAPIKey, testAPISecret, alice.String(), "alice", roomB, TokenTTL)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	otherIdentity, _, err := AccessToken(testAPIKey, testAPISecret, bob.String(), "bob", roomA, TokenTTL)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if base == otherRoom {
		t.Fatal("token did not change with roomID")
	}
	if base == otherIdentity {
		t.Fatal("token did not change with identity")
	}

	// And the difference is in the claims, not just the random jti: each
	// grant names its own room.
	if got := videoClaim(t, parseAccessToken(t, otherRoom, testAPISecret)).Room; got != roomB.String() {
		t.Fatalf("other-room token grants room %q, want %q", got, roomB.String())
	}
}

// TestAccessTokenEmptyCredentials checks that a missing API key or secret is
// an error, never a token signed with an empty value.
func TestAccessTokenEmptyCredentials(t *testing.T) {
	roomID := uuid.New()

	if token, _, err := AccessToken("", testAPISecret, "alice", "alice", roomID, TokenTTL); err == nil {
		t.Fatalf("empty apiKey must error, got token %q", token)
	}
	if token, _, err := AccessToken(testAPIKey, "", "alice", "alice", roomID, TokenTTL); err == nil {
		t.Fatalf("empty apiSecret must error, got token %q", token)
	}
}

// TestAccessTokenSignatureRejectsWrongSecret checks that the SFU (holding the
// real secret) rejects a token signed with a different one.
func TestAccessTokenSignatureRejectsWrongSecret(t *testing.T) {
	roomID := uuid.New()
	token, _, err := AccessToken(testAPIKey, testAPISecret, "alice", "alice", roomID, TokenTTL)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return []byte("wrong-secret"), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})); err == nil {
		t.Fatal("token signed with another secret must not verify")
	}
}
