package httpapi

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/config"
)

// CallDeps bundles the dependencies for the call (WebRTC) handlers.
type CallDeps struct {
	// ICE carries the STUN/TURN server settings; see config.ICEConfig.
	ICE config.ICEConfig
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
