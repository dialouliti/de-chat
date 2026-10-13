# deChat client — next steps

This document tracks the roadmap for the web client (in `web/`) from the current
base (login + rooms + chat) toward the future features: push notifications,
1:1 calls, group calls, screen-share, and streaming. It also captures the
backend changes each phase needs, so server and client work stays aligned.

The base client was chosen to be a **single React + TypeScript PWA** (see the
architecture discussion that led here) precisely because every future media
feature (calls, webcam, screen-share) is a first-class browser API. No second
codebase should be needed; native wrappers (Capacitor/Tauri) are optional and
additive.

---

## Phase 0 — where we are now

✅ Login (`POST /api/v1/login` → JWT)
✅ Room list (`GET /api/v1/rooms`, TanStack Query)
✅ Message history (`GET /api/v1/rooms/{id}/messages`)
✅ Real-time chat (WebSocket `/api/v1/ws`)
✅ Installable PWA (service worker precaches the shell)
✅ Generated typed API client (`openapi-typescript` from `openapi.json`)
✅ Backend CORS support (`CORS_ORIGIN` env)

The two binary clients (`dechat-client`, `dechat-admin`) remain; the web client
is the primary UI for non-admin users going forward.

---

## Phase 1 — UX polish & robustness (no backend changes)

These improve the base chat experience. All client-side.

- [x] **Reconnect with backfill.** ✅ Done — `after` cursor on
      `GET /rooms/{id}/messages` returns messages newer than the room's
      high-water mark, oldest first; the client fetches it on WS reconnect.
- [ ] **Optimistic send.** Show the user's own message immediately with a
      "sending" state, then reconcile when the broadcast arrives (match by
      content+timestamp, or by a client-generated temp id). Currently the
      message only appears after the server round-trip.
- [ ] **Load older messages (infinite scroll up).** When the user scrolls to
      the top, fetch the next page using the oldest visible message's
      `created_at` as the `before` cursor.
- [ ] **Markdown / link rendering.** Messages are plain text today; a
      lightweight renderer (e.g. `marked` + DOMPurify) would help. Keep it
      minimal — no raw HTML.
- [ ] **Persisted "last active room".** Remember which room the user was in
      across reloads (Zustand `persist` middleware).
- [x] **Typing indicator.** ✅ Done — WS `typing` message type, debounced
      server-side, with auto-clear on the client.
- [x] **Online status / presence.** ✅ Done — hub user-id index +
      `presence` broadcasts on connect/disconnect + snapshot on connect.
- [x] **Read receipts.** ✅ Done — per-(user, room) `last_read_at` cursor;
      sender sees "Read by N" derived from it.
- [x] **Edit message.** ✅ Done — WS `edit` message type; author-or-admin;
      `edited_at` persisted; inline edit UI.
- [x] **Room roles.** ✅ Done — `room_members` table (owner/member,
      moderation-only); owner can delete/clear their room and manage members;
      global admins override.
- [x] **Emoji reactions.** ✅ Done — WS `reaction` toggle (per-user, per-emoji);
      history returns aggregated per-message `reactions`; chip UI with count +
      a quick-pick emoji palette.
- [x] **Unread counters.** ✅ Done — `GET /api/v1/me/unread` + WS-driven
      increments for background rooms; room-list badges (99+ format) and a
      "(N) deChat" tab title.
- [x] **In-app notifications.** ✅ Done — Notification API fires for incoming
      messages while the tab is hidden (permission requested on first send,
      not on load); tab title shows the unread count.

---

## Phase 2 — push notifications (small backend addition)

> **Status split with Phase 1:** the in-app notification MVP is done (see the
> ✅ above) — notifications for messages that arrive while the tab is open but
> hidden, plus the unread tab title. What remains **in this phase** is Web
> Push: service-worker-delivered notifications that work when the tab is
> closed entirely.

Web Push lets the PWA show notifications even when the tab is closed (on
desktop; on iOS, only when added to home screen).

**Backend:**
- Add a `push_subscriptions` table (`user_id`, `endpoint`, `p256dh`, `auth`,
  `created_at`).
- `POST /api/v1/me/push-subscription` — store a subscription.
- `DELETE /api/v1/me/push-subscription` — remove one.
- A notification sender: when a message is persisted, enqueue a notification
  to all subscribers in that room (except the sender). VAPID-signed via
  `web-push-go` (a Go library that implements the Web Push protocol).
- Admin generates a VAPID keypair; public key served at
  `GET /api/v1/push/vapid-public-key`.

**Client:**
- Request Notification permission on first message send (not on load — that's
  aggressive).
- `serviceWorkerRegistration.pushManager.subscribe({ userVisibleOnly: true,
  applicationServerKey })` → POST the subscription to the backend.
- A service worker `push` event handler that posts a notification, and a
  `notificationclick` handler that focuses the room.

**Gotcha:** iOS Web Push requires the app to be added to the Home Screen and
only works in the foreground or shortly after backgrounding. Full background
push on iOS still needs a native wrapper (Phase 5). This is a known PWA
limitation, not a bug.

---

## Phase 3 — 1:1 calls (WebRTC over the existing WebSocket)

This is the entry point for media. 1:1 calls are **peer-to-peer** — no SFU
needed. The hard part is signaling, and the existing WebSocket hub already
provides the transport.

### 3a. Signaling protocol (backend change, small)

Add new `type` values to the existing `ClientMsg`/`ServerMsg` WS envelopes
(`internal/models/models.go`). These flow over the **same** WebSocket as
chat — no new endpoint:

```
ClientMsg types (add):
  "call_offer"     { to: <user_id>, sdp: <offer>, call_id: <uuid> }
  "call_answer"    { to: <user_id>, sdp: <answer>, call_id: <uuid> }
  "call_ice"       { to: <user_id>, candidate: <ice>, call_id: <uuid> }
  "call_end"       { to: <user_id>, call_id: <uuid> }

ServerMsg types (add):
  "call_offer"     { from: <user_id>, sdp, call_id }
  "call_answer"    { from: <user_id>, sdp, call_id }
  "call_ice"       { from: <user_id>, candidate, call_id }
  "call_end"       { from: <user_id>, call_id }
  "call_ringing"   { call_id }   // optional: callee acknowledged
```

**Routing:** the hub currently broadcasts to all clients. For calls, the WS
handler needs to look up the recipient's active connection by `user_id`. This
requires a small `user_id → client` index in the hub (the hub already tracks
clients; it just needs the mapping). Messages not matching a known recipient
are dropped (or returned as a `call_error`).

> Note: the v1 rule "all users see all rooms" means a user can be reached as
> long as they're connected. Presence (who's online) is a natural follow-on:
> a `presence` message on connect/disconnect would let the client show call
> buttons only for available users.

### 3b. NAT traversal: STUN/TURN (infra)

Add **coturn** to `docker-compose.yml`:

```yaml
coturn:
  image: coturn/coturn:4-alpine
  network_mode: host   # TURN needs the host's real IP for relaying
  command: ["turnserver", "-a", "-o", "-v", "--no-dtls", "--no-tls",
            "-u", "dechat:secret", "-r", "dechat"]
  ports:
    - "3478:3478/udp"   # TURN/STUN
```

The client passes TURN servers into `RTCPeerConnection`'s `iceServers`. Without
this, calls only work when both peers are on the same network — fine for local
dev, useless in production. STUN alone (~80% of cases) is free; TURN relay
(~the other 20%) costs bandwidth, so plan for it.

### 3c. Client (`web/src/rtc/`)

- `peer.ts` — `RTCPeerConnection` wrapper: creates the connection, wires ICE
  candidates to the signaling channel, manages track add/remove.
- `media.ts` — `getUserMedia({ video, audio })` for cam+mic; expose local
  preview via a `<video>` element attached to the returned `MediaStream`.
- `signaling.ts` — translates `call_offer`/`call_answer`/`call_ice` WS
  messages into `peer.ts` calls. Depends on `ws/socket.ts`, never the reverse.
- `CallView.tsx` — UI: incoming-call prompt, in-call view (two `<video>`
  tiles), mute/camera-off/hangup controls.

Key implementation notes:
- **Perfect negotiation** pattern to avoid glare (both ends calling at once).
- Reuse **one** `RTCPeerConnection` per call; create a new one per call, never
  per ICE candidate.
- On `call_end`, close the `RTCPeerConnection` and stop all tracks (otherwise
  the camera light stays on).

### 3d. Backend scope for Phase 3
- New `ClientMsg`/`ServerMsg` types (a few lines each).
- Hub: a `user_id → client` map for targeted delivery (the only structural
  hub change needed through all of phase 3).
- `coturn` in `docker-compose.yml` + TURN credentials surfaced to the client
  (e.g. via `GET /api/v1/call/config` or a field on `/me`).
- No new tables, no persistence.

---

## Phase 4 — group calls + screen-share (the SFU decision point)

3+ participant WebRTC meshes don't scale (each peer N-1 connections; CPU and
bandwidth explode). This phase needs a **Selective Forwarding Unit (SFU)**.
The SFU choice is the single biggest infra decision in the roadmap — make it
*here*, not earlier.

### SFU options

| SFU | Language | Fits because… | Trade-off |
|-----|----------|---------------|-----------|
| **LiveKit** | Go server, JS SDK | Easiest to ship; great JS SDK; Go server matches your stack | One more service to operate; opinionated |
| **Pion SFU** | Pure Go | Most cohesive with the Go backend; full control | More hand-rolled; you build more of the room logic |
| **mediasoup** | Node (C++ core) | Very performant; mature | Adds a Node process; signaling glue |
| **Janus** | C | Mature, feature-rich | Heavier to operate; C extension surface |

**Recommendation:** start with **LiveKit** if you want to ship fast and don't
mind a separate service; choose **Pion** if you want everything in Go and are
willing to write more. Both can run alongside the chat server in
`docker-compose.yml`. The *client* swap is contained to `web/src/rtc/` — the
chat layer is unaffected either way.

### Screen-share

`navigator.mediaDevices.getDisplayMedia()` — desktop browsers only (Chrome,
Firefox, Edge; Safari 13+). Mobile browsers don't support it, and that's true
regardless of stack. The returned `MediaStream` is added to the peer
connection the same way as a camera track; the SFU forwards it. UI: a "Share
screen" toggle that adds the display track and labels it in the participant
grid.

### Recording / streaming

`MediaRecorder` on the local stream, or SFU-side recording (LiveKit and
mediasoup both support it). SFU-side is better for group calls (one recording
source). Persisted like messages — add a `recordings` table if retention is
needed.

### Backend scope for Phase 4
- The SFU service in `docker-compose.yml`.
- A room↔SFU-room mapping: when a user joins a call in chat room X, the
  backend mints (or reuses) an SFU room token for that chat room and returns
  it via the WS or a `POST /api/v1/rooms/{id}/call` endpoint.
- Optional: a `calls` table recording call start/end and participants, for
  history.

---

## Phase 5 — native wrappers (optional, only if needed)

If a genuine limitation appears that the PWA can't meet, wrap the **same**
web codebase — do not rewrite:

- **iOS background audio** — the one real PWA gap. iOS suspends PWAs in the
  background, dropping active calls. A **Capacitor** wrapper gets proper iOS
  audio-session handling. Same codebase, one extra `ios/` and `android/`
  platform folder, distributed via the App Store / Play Store.
- **Desktop native** — if a tray-icon / always-on-top window is wanted,
  **Tauri 2** (Rust shell, ~10 MB) or **Electron**. Tauri also does mobile.

Both wrappers reuse 100% of the `web/` code. The decision to wrap is
feature-driven, not stack-driven — only adopt when a concrete need appears.

---

## Cross-cutting: protocol versioning

Once calls land (Phase 3), the WS protocol stops being trivial. Consider:
- A `protocol_version` field on connect, so the server can reject or warn on
  mismatched clients.
- Feature flags surfaced via `/me` or a `/config` endpoint (e.g.
  `{ calls_enabled: true, push_enabled: false }`) so the client can hide UI
  for features the deployment doesn't support.

---

## Suggested order

1. **Phase 1** (UX polish) — pure client, immediate value, no protocol risk.
2. **Phase 3a+3b** (signaling + TURN) — lay the call foundation even if the
   call UI is rough; it unblocks all later media work.
3. **Phase 3c** (1:1 call UI) — first media milestone.
4. **Phase 2** (push) — independent, can be done in parallel with 3.
5. **Phase 4** (group calls / SFU) — only when 1:1 calls are proven and you
   know your participant counts.
6. **Phase 5** (native wrappers) — only when a PWA limitation actually bites.

The important property: **Phases 1–3 require no backend rewrites and no new
infrastructure beyond coturn.** The current REST + WS architecture carries the
project through 1:1 calls. Only group calls (Phase 4) need a real new
component (the SFU), and by then the participant-count and latency data from
real usage will inform the SFU choice.
