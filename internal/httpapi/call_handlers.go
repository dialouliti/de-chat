package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/config"
	"github.com/pavelananev/dechat/internal/livekit"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

// RoomGetter is the store surface the call handlers need: verifying a room
// exists before minting a call token for it. *postgres.Store satisfies it;
// the narrow interface (in the spirit of ws.MessageStore) keeps the handler
// testable without a database.
type RoomGetter interface {
	GetRoomByID(ctx context.Context, id uuid.UUID) (models.Room, error)
}

// CallDeps bundles the dependencies for the call (WebRTC) handlers.
type CallDeps struct {
	// ICE carries the STUN/TURN server settings; see config.ICEConfig.
	ICE config.ICEConfig
	// Store answers "does this room exist" before a token is minted.
	Store RoomGetter
	// LiveKit carries the SFU URL + API pair; an empty APIKey means the SFU
	// feature is disabled and StartCall answers 503.
	LiveKit config.LiveKitConfig
}

// iceServer mirrors the browser's RTCIceServer: one or more server URLs plus
// optional credentials. STUN entries carry no credentials; TURN entries carry
// short-lived ones regenerated on every request.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// callConfigResponse is the body of GET /api/v1/call/config.
type callConfigResponse struct {
	// TTLSeconds is how long the TURN credentials stay valid.
	TTLSeconds int64       `json:"ttl_seconds"`
	ICEServers []iceServer `json:"ice_servers"`
}

// callTokenResponse is the body of POST /api/v1/rooms/{id}/call.
type callTokenResponse struct {
	// URL is the LiveKit signaling URL to connect the token to.
	URL string `json:"url"`
	// Token is a signed LiveKit access JWT for the room's call.
	Token string `json:"token"`
	// ExpiresAt is when the SFU stops accepting the token (RFC 3339).
	ExpiresAt time.Time `json:"expires_at"`
}

// CallConfig serves the ICE (STUN/TURN) configuration for WebRTC calls.
// STUN is always present. When TURN is configured, the TURN entry carries
// fresh short-lived credentials bound to the caller's username, generated
// per request — clients never cache them longer than ttl_seconds.
func (d CallDeps) CallConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	resp := callConfigResponse{
		TTLSeconds: int64(d.ICE.TURNTTL / time.Second),
		ICEServers: []iceServer{{URLs: d.ICE.STUNURLs}},
	}
	if len(d.ICE.TURNURLs) > 0 {
		username, credential := turnCredentials(
			d.ICE.TURNSecret, claims.Username, time.Now().Add(d.ICE.TURNTTL),
		)
		resp.ICEServers = append(resp.ICEServers, iceServer{
			URLs:       d.ICE.TURNURLs,
			Username:   username,
			Credential: credential,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// turnCredentials generates a username/credential pair per coturn's REST API
// (the "time-limited credentials" scheme, draft-uberti-behave-turn-rest):
//
//	username    = "<unix-expiry>:<username>"   (expiry = now + ttl)
//	credential  = base64(HMAC-SHA1(secret, username))
//
// coturn validates the pair by recomputing the HMAC from the shared secret
// and rejecting usernames whose expiry is in the past.
func turnCredentials(secret, username string, expiry time.Time) (string, string) {
	turnUsername := strconv.FormatInt(expiry.Unix(), 10) + ":" + username
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(turnUsername))
	return turnUsername, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// StartCall mints a LiveKit access token for the room's group call. The
// token's video grant is scoped to the single room (join + publish +
// subscribe + data), with the caller's user id as identity and username as
// display name; clients connect it to the returned SFU URL. Answered 503
// when the server has no LiveKit API pair configured (SFU disabled).
func (d CallDeps) StartCall(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	roomID, ok := parseUUID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if d.LiveKit.APIKey == "" {
		jsonError(w, http.StatusServiceUnavailable, "livekit sfu is disabled")
		return
	}
	if _, err := d.Store.GetRoomByID(r.Context(), roomID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "room not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to find room")
		return
	}

	token, expiresAt, err := livekit.AccessToken(
		d.LiveKit.APIKey, d.LiveKit.APISecret,
		claims.UserID.String(), claims.Username, roomID, livekit.TokenTTL,
	)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to mint call token")
		return
	}
	writeJSON(w, http.StatusOK, callTokenResponse{URL: d.LiveKit.URL, Token: token, ExpiresAt: expiresAt})
}
