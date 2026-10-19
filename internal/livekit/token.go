// Package livekit mints LiveKit SFU access tokens: signed JWTs carrying a
// video grant scoped to a single room. The token format follows LiveKit's
// access token spec (https://docs.livekit.io/realserver/generating-tokens/):
// the API key is the issuer, the API secret is the HMAC signing key.
package livekit

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenTTL is how long a minted access token stays valid. It is deliberately
// generous (a whole afternoon of calls) yet bounded — a leaked token stops
// working on its own; clients simply call the endpoint again for a fresh one.
// Not configurable on purpose: a shorter TTL belongs to a future "refresh"
// card, not another env knob.
const TokenTTL = 6 * time.Hour

// videoGrant is the LiveKit grant structure carried in the token's "video"
// claim. It scopes the holder to exactly one room with full media rights.
type videoGrant struct {
	Room           string `json:"room"`
	RoomJoin       bool   `json:"roomJoin"`
	CanPublish     bool   `json:"canPublish"`
	CanSubscribe   bool   `json:"canSubscribe"`
	CanPublishData bool   `json:"canPublishData"`
}

// accessTokenClaims is the payload of a LiveKit access token: the standard
// JWT registered claims plus the display name and the video grant.
type accessTokenClaims struct {
	Name  string     `json:"name,omitempty"`
	Video videoGrant `json:"video"`
	jwt.RegisteredClaims
}

// AccessToken mints a signed LiveKit access token for joining roomID's call.
//
// The token is a JWT (HS256, signed with apiSecret) whose video grant allows
// joining, publishing and subscribing in that one room only. identity lands
// in "sub" (the caller's user id at the call site) and name in "name" (the
// display name other participants see). The returned expiresAt is the exp
// claim, i.e. now + ttl.
//
// An empty apiKey or apiSecret is a configuration error (the SFU feature is
// disabled) and returns an error rather than a token signed with nothing.
func AccessToken(apiKey, apiSecret, identity, name string, roomID uuid.UUID, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	if apiKey == "" || apiSecret == "" {
		return "", time.Time{}, errors.New("livekit: api key and secret must not be empty")
	}

	now := time.Now()
	expiresAt = now.Add(ttl)
	claims := accessTokenClaims{
		Name: name,
		Video: videoGrant{
			Room:           roomID.String(),
			RoomJoin:       true,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    apiKey,
			Subject:   identity,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(apiSecret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("livekit: sign token: %w", err)
	}
	return signed, expiresAt, nil
}
