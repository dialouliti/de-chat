# deChat

A real-time chat **backend** for a private group, written in Go. It exposes a
JSON REST API and a WebSocket endpoint for live message delivery. There is
**no open registration**: an administrator creates all user accounts and all
rooms. A React + TypeScript **PWA client** lives in [`web/`](web/), and two
console clients (`dechat-client`, `dechat-admin`) are provided for terminal
chat and administration.

## Features (v1)

- Login with username + password → JWT
- Admin-managed users (create / list / delete / block)
- Admin-managed rooms (create / delete / clear)
- Real-time messages over WebSocket
- PostgreSQL-backed message history with cursor pagination
- Messages are kept indefinitely (admin can clear a room on demand)
- All authenticated users can see all rooms
- **Presence** — see who's online
- **Typing indicators** — real-time "Alice is typing…"
- **Read receipts** — sender sees "Read by N"
- **Message editing** — edit your own messages (`edited_at` tracked)
- **Emoji reactions** — toggle 👍 ❤️ … on any message (WS `reaction`;
  history returns them aggregated per message)
- **Unread counts** — per-room counts of other users' unread messages
  (`GET /api/v1/me/unread`)
- **Room roles** — per-room owner/member (moderation-only); owners can
  delete/clear their rooms and manage members

Designed so that future **calls / group calls** (WebRTC signaling) can reuse
the same room + hub model.

---

## Install deps and requirements

You need the toolchain for whichever parts you plan to run.

### Backend (server + console clients)

- **Go 1.21+**
- **PostgreSQL 16** (a reachable instance; the app applies migrations itself)
- **Docker** (only if you use `make docker-up`)

Install Go dependencies:

```bash
make tidy
```

### Web PWA client (`web/`)

- **Node 22.13+** (Node 24 used in development and in the Docker image)
- **pnpm 10.5+** (pnpm 11.x used in development and in the Docker image)

```bash
cd web
pnpm install          # installs deps (workspace allows esbuild's build)
```

---

## Run server

### Option A — Docker Compose (easiest; full stack)

> Start the Docker app first if the daemon isn't running.

```bash
cp .env.example .env
# Edit .env: set a strong JWT_SECRET (at least 32 bytes — e.g.
#   openssl rand -base64 32
# the server refuses to start with a shorter or default value). Leave
# ADMIN_PASSWORD empty to have a random one generated and logged on first
# startup.

make docker-up          # builds db + app + web + coturn + livekit, runs migrations, boots the stack
```

On first boot the logs print the bootstrap admin credentials (if
`ADMIN_PASSWORD` was empty) — save them. The stack then serves:

- **https://localhost** — the Caddy edge (`web` service): the PWA, the REST
  API, and the WebSocket (`wss://…/api/v1/ws`) on one origin. For
  `localhost` the certificate is self-signed (Caddy's internal CA), so
  expect a one-time browser warning and use `curl -k`. The WebSocket
  upgrade rides HTTP/1.1 — exactly what the browser WebSocket API uses;
  clients forcing WebSocket over HTTP/2 (RFC 8441) get a 400, because
  Caddy terminates h2/h3 and the classic RFC 6455 upgrade is
  HTTP/1.1-only.
- **http://127.0.0.1:8080** — the Go API directly, published on loopback
  only (for debugging; bypasses TLS).

For a public deployment set `DOMAIN` to your DNS name — ports 80/443 must
be reachable from the internet, and Caddy obtains a Let's Encrypt
certificate automatically. Optionally add an ACME email as a single
`email you@example.com` line in the global block of the `Caddyfile`.

**TURN for calls (optional).** The stack always runs a coturn service
(STUN/TURN on 3478 udp+tcp, relay range 49160–49200/udp, short-lived
credentials from a `TURN_SECRET` shared with the app; relaying into
private, CGNAT and link-local IP ranges, IPv4 and IPv6, is denied). Its
ports are published on loopback only by default — set
`COTURN_BIND_ADDR=0.0.0.0` in `.env` to test calls from other LAN
devices, and a strong `TURN_SECRET` when you do. Clients only use coturn
when `TURN_URLS` is set — to enable locally, add
`TURN_URLS=turn:localhost:3478?transport=udp` to `.env` and re-run
`make docker-up`; `GET /api/v1/call/config` then includes a TURN entry with
fresh credentials. Notes: TURN over TLS (5349/443) is not configured yet;
overriding the image's default command drops its
`--external-ip=$(detect-external-ip)` autodetection — for a NATed/public
deployment add `--external-ip=<public-ip>` to the coturn command, publish
the ports beyond loopback (`COTURN_BIND_ADDR`), and keep the relay range
reachable; for public deployments set a
strong `TURN_SECRET` — the value must not contain `$`, backticks or glob
characters (the image entrypoint shell-expands command args;
`openssl rand -base64 32` is safe).

**LiveKit SFU (for upcoming group calls).** The stack also runs a LiveKit
server (`livekit/livekit-server:v1.13.10`, config in
[`livekit.yaml`](livekit.yaml)) — the selective forwarding unit that will
route media for group calls of 3+ participants. The current 1:1 P2P calls
don't use it, and no API endpoint talks to it yet (token minting is the
next card). `make docker-up` always starts it: signaling on
`ws://localhost:7880`, ICE-TCP on 7881, and a narrow 50100–50200/udp media
range. Ports are published on loopback only by default — set
`LIVEKIT_BIND_ADDR=0.0.0.0` in `.env` (plus a strong `LIVEKIT_API_SECRET`,
and `rtc.node_ip` in `livekit.yaml` set to the machine's LAN IP) to test
from other LAN devices. For a public deployment see the notes in
`livekit.yaml` (`use_external_ip`, firewall ports) — LiveKit's docs
recommend host networking there, which Docker Desktop (mac/win) cannot
provide.

Stop with `make docker-down`.

### Option B — local (without Docker)

You need Go 1.21+ and a reachable PostgreSQL instance.

```bash
# point the app at your DB and set a strong JWT secret
export DATABASE_URL="postgres://dechat:dechat@localhost:5432/dechat?sslmode=disable"
export JWT_SECRET="$(openssl rand -base64 32)"

make run                # go run ./cmd/server (reads environment variables)
```

Migrations are applied automatically on startup. The API listens on
`http://localhost:8080` by default (`SERVER_ADDR`).

---

## Run PWA

The web client expects the backend running on `http://localhost:8080`
(e.g. `make docker-up` from the repo root). In dev, Vite proxies `/api`,
`/openapi.json`, etc. to the backend, so no env config is needed.

```bash
cd web
pnpm install          # first time
pnpm gen:api          # generates src/api/schema.ts from ../openapi.json
pnpm gen:icons        # generates placeholder PWA icons (run once)

pnpm dev              # Vite dev server on http://localhost:5173
```

If `pnpm build`/`pnpm dev` fails with esbuild missing, run
`pnpm rebuild esbuild` (or `pnpm add -D @esbuild/<your-platform>`).

A production build doesn't require any of this locally: the `web` service
in `docker-compose.yml` builds the PWA inside its Docker image and serves it
from the same origin as the API over HTTPS (`https://localhost`) — no
`VITE_API_URL` needed. The dev flow above is unchanged.

Other useful commands:

```bash
pnpm typecheck        # tsc --noEmit
pnpm build            # production build → web/dist/
pnpm preview          # serve the production build
```

See [`web/README.md`](web/README.md) for auth flow, env vars, and project layout.

---

## Run console clients

Two line-based terminal clients are provided. Server URL can be preset with
`DECHAT_URL` or the `-server` flag to skip the prompt.

### Chat client (`cmd/client`)

Log in, pick a room, and chat live from the terminal.

```bash
make build-client        # produces ./bin/dechat-client
./bin/dechat-client      # prompts for server URL, username, password

# or run directly:
make run-client
DECHAT_URL=http://localhost:8080 ./bin/dechat-client
```

In a room, type a message and press Enter to send. Commands:

- `/back` — leave the room and return to the room picker
- `/quit` — exit the client
- `Ctrl+C` — exit at any time

Recent history (last 50 messages) loads when you enter a room.

### Admin client (`cmd/admin`)

For administrators to manage rooms and users. Requires an admin account
(verified server-side on login; a non-admin account is rejected).

```bash
make build-admin         # produces ./bin/dechat-admin
./bin/dechat-admin       # prompts for server URL, username, password

# or run directly:
make run-admin
```

Main menu:

- **Rooms** — list, create, delete (also removes the room's messages), clear
- **Users** — list (blocked users marked `[BLOCKED]`), create (with a
  `user`/`admin` role), delete, block / unblock

Blocking a user revokes access immediately: they can no longer log in,
existing JWTs stop working, and their WebSocket connection is rejected on the
next reconnect.

---

## Configuration

All settings come from environment variables (see `.env.example`):

| Variable         | Default     | Notes                                              |
|------------------|-------------|----------------------------------------------------|
| `SERVER_ADDR`    | `:8080`     | Listen address                                     |
| `DATABASE_URL`   | *(none)*    | PostgreSQL connection string (required)            |
| `JWT_SECRET`     | *(none)*    | HMAC signing key (**required**, min 32 bytes; server refuses to boot on a weak/default value)  |
| `JWT_TTL`        | `24h`       | Token lifetime                                     |
| `ADMIN_USERNAME` | `admin`     | Bootstrap admin username                           |
| `ADMIN_PASSWORD` | *(empty)*   | Bootstrap admin password; random if empty          |
| `LOG_LEVEL`      | `info`      | `debug`/`info`/`warn`/`error`                      |
| `CORS_ORIGIN`    | `*`         | Browser CORS: `*`, a single origin, or a list      |
| `STUN_URLS`      | `stun:stun.l.google.com:19302` | Comma-separated STUN URLs, always served |
| `TURN_URLS`      | *(empty)*   | Comma-separated TURN URLs; empty = TURN disabled. Public: `turn:turn.example.com:3478?transport=udp`; local compose stack: `turn:localhost:3478?transport=udp` |
| `TURN_SECRET`    | *(empty)*   | coturn shared secret for short-lived credentials. **Required** when TURN_URLS is set when running outside docker compose — the server refuses to boot and reads no .env file itself; the `${VAR:-default}` substitution happens only in docker compose, which fills an empty value with the dev default `dechat-dev-turn-secret-change-me` shared by the app and coturn services. Override with `openssl rand -base64 32` for public deployments; avoid `$`, backticks and globs in the value (the coturn entrypoint shell-expands command args) |
| `TURN_TTL`       | `1h`        | Lifetime of generated TURN credentials             |
| `DOMAIN`         | `localhost` | Site name of the Caddy edge (web service); a real DNS name switches it to automatic Let's Encrypt |
| `COTURN_BIND_ADDR` | `127.0.0.1` | Host address coturn's ports (3478 + relay range) are published on (docker compose only); `0.0.0.0` for LAN devices — then set a strong `TURN_SECRET` |
| `LIVEKIT_API_KEY` | *(empty)* | LiveKit SFU API key. Must be set together with `LIVEKIT_API_SECRET` — exactly one of the two makes the server refuse to boot; empty pair = SFU feature disabled. Docker compose fills the dev default `devkey` (shared by the app and livekit services); the server itself reads no `.env` file |
| `LIVEKIT_API_SECRET` | *(empty)* | LiveKit SFU API secret (the token-minting endpoint is a follow-up card); same pair rule as the key. Compose dev default `dechat-dev-livekit-secret-change-me` — override with `openssl rand -base64 32` for anything beyond loopback |
| `LIVEKIT_BIND_ADDR` | `127.0.0.1` | Host address the LiveKit ports (7880, 7881, 50100–50200/udp) are published on (docker compose only); `0.0.0.0` for LAN devices — then set a strong `LIVEKIT_API_SECRET` and `rtc.node_ip` in `livekit.yaml` |

## OpenAPI spec

The server serves an **autogenerated** OpenAPI 3.0 document, built at runtime
from the actual Go request/response structs — so the schemas can never drift
from what the handlers marshal. No annotations, no separate codegen step.

| Path              | What                                                    |
|-------------------|---------------------------------------------------------|
| `GET /openapi.json` | The spec, as JSON (CORS-enabled for any origin)       |
| `GET /openapi.yaml` | The same spec, as YAML                                 |
| `GET /docs`        | Interactive Swagger UI (loads the UI from a CDN)       |

The Swagger UI page loads its assets from `cdn.jsdelivr.net`, so the browser
needs outbound internet. The raw `/openapi.json` and `/openapi.yaml` endpoints
work fully offline.

To dump a copy of the spec to disk (e.g. to feed a code generator, or to
check it into a separate repo):

```bash
make openapi     # writes ./openapi.json and ./openapi.yaml
```

The WebSocket endpoint (`GET /api/v1/ws`) is not modeled as a path in the spec
— OpenAPI 3.0 has no concept of WebSocket. It is documented in prose in the
spec's `info.description`, and its `ClientMsg` / `ServerMsg` envelope types
are registered under `components.schemas` so WS clients still get typed
schemas to codegen against.

The web client's typed API layer is generated from this spec:

```bash
# from the repo root, after the API changes
make openapi          # regenerate openapi.json from the Go server
cd web && pnpm gen:api  # regenerate src/api/schema.ts
pnpm typecheck        # surface any breaking changes
```

## REST API

All endpoints are under `/api/v1`. Protected endpoints require
`Authorization: Bearer <jwt>`.

### Auth

| Method | Path              | Auth | Body / Notes                         |
|--------|-------------------|------|--------------------------------------|
| POST   | `/login`          | no   | `{username, password}` → `{token, username, role}` |
| GET    | `/me`             | yes  | Returns the caller's profile         |
| GET    | `/me/unread`      | yes  | Unread counts per room: `[{room_id, unread_count}]`; only other users' messages newer than your read cursor count; rooms with zero unread are omitted (`[]` when nothing is unread) |

### Calls (WebRTC)

| Method | Path           | Auth | Body / Notes                         |
|--------|----------------|------|--------------------------------------|
| GET    | `/call/config` | yes  | ICE servers for voice calls: `{ttl_seconds, ice_servers: [{urls, username?, credential?}]}`. A STUN entry is always present; a TURN entry is added only when `TURN_URLS` is configured, carrying fresh short-lived credentials (coturn REST API: username `"<unix-expiry>:<caller>"`, credential `base64(HMAC-SHA1(TURN_SECRET, username))`) regenerated on every request — refetch before `ttl_seconds` elapses, don't cache long-term |

### Rooms & messages (any authenticated user)

| Method | Path                              | Notes                                             |
|--------|-----------------------------------|---------------------------------------------------|
| GET    | `/rooms`                          | List all rooms                                    |
| GET    | `/rooms/{id}/messages`            | Message history, paginated (`before` cursor or none: newest-first; `after` cursor: oldest first); each message includes aggregated `reactions` (`[{emoji, user_id, username}]`, absent when none) |
| GET    | `/rooms/{id}/members`             | List room members + roles                         |
| DELETE | `/rooms/{id}`                     | Delete room (owner or admin)                      |
| POST   | `/rooms/{id}/clear`               | Clear messages, keep room (owner or admin)        |
| POST   | `/rooms/{id}/members`             | Add/update member (owner or admin)                |
| DELETE | `/rooms/{id}/members/{userId}`    | Remove member (owner or admin)                    |

`/rooms/{id}/messages` query params:

- `limit` — items per page (1–100, default 50)
- `before` — RFC 3339 timestamp cursor; returns messages older than this, newest first
- `after` — RFC 3339 timestamp cursor; returns messages newer than this, oldest first (used to backfill after a WS reconnect); mutually exclusive with `before` (400)

### Admin only

| Method | Path                       | Body / Notes                                  |
|--------|----------------------------|-----------------------------------------------|
| POST   | `/rooms`                   | `{name, description}` → room (creator = owner) |
| POST   | `/admin/users`             | `{username, password, role?}` → user          |
| GET    | `/admin/users`             | List all users                                |
| DELETE | `/admin/users/{id}`        | Deletes user (and their messages via cascade) |
| POST   | `/admin/users/{id}/block`  | Blocks user; revokes access immediately       |
| POST   | `/admin/users/{id}/unblock`| Re-enables a blocked user                     |

`role` is `user` (default) or `admin`. A blocked user cannot log in and is
rejected on every authenticated request and on the WebSocket handshake — so
blocking takes effect immediately even for users holding a still-valid JWT
(existing WebSocket connections drop on their next reconnect).

### Health

`GET /healthz` → `200 ok` (no auth) for orchestrators.

## WebSocket — real-time messages

Connect with the JWT as a query parameter:

```
GET ws://localhost:8080/api/v1/ws?token=<jwt>
```

Connection is upgraded only if the token is valid. Server sends periodic
`ping` frames; clients should answer with `pong` (most WebSocket libraries do
this automatically).

### Client → Server

Send a chat message:

```json
{ "type": "message", "room_id": "<uuid>", "content": "hello" }
```

Edit your own message (admins can edit anyone's). **A message can only be
edited once** — a second edit attempt is rejected with an error:

```json
{ "type": "edit", "room_id": "<uuid>", "message_id": "<uuid>", "content": "new text" }
```

Toggle an emoji reaction on a message (re-sending the same emoji removes it;
the emoji must be non-empty, ≤ 32 bytes, no control characters):

```json
{ "type": "reaction", "room_id": "<uuid>", "message_id": "<uuid>", "emoji": "👍" }
```

Typing indicator (debounced server-side — only emitted on a real state change):

```json
{ "type": "typing", "room_id": "<uuid>", "is_typing": true }
```

Mark a room as read (advances your read cursor; triggers a `read_receipt` broadcast):

```json
{ "type": "read", "room_id": "<uuid>" }
```

Lightweight ping:

```json
{ "type": "ping" }
```

### Server → Client

New message (broadcast to everyone connected to the room, **after** it is
persisted to the database):

```json
{
  "type": "message",
  "message": {
    "id": "<uuid>", "room_id": "<uuid>", "user_id": "<uuid>",
    "username": "alice", "content": "hello",
    "created_at": "2026-07-04T12:00:00Z"
  }
}
```

Edited message (broadcast with the updated row; `edited_at` is set):

```json
{
  "type": "message_edited", "message_id": "<uuid>",
  "message": { "id": "<uuid>", "content": "new text", "edited_at": "2026-07-04T12:01:00Z", ... }
}
```

Reaction toggled (broadcast to the room; `reaction_added` is `true` when the
reaction was inserted, `false` when it was removed):

```json
{ "type": "reaction", "room_id": "<uuid>", "message_id": "<uuid>", "user_id": "<uuid>", "username": "alice", "emoji": "👍", "reaction_added": true }
```

Typing indicator (broadcast to the room):

```json
{ "type": "typing", "room_id": "<uuid>", "user_id": "<uuid>", "username": "alice", "is_typing": true }
```

Presence (global — on connect/disconnect; `online_users` only on initial snapshot):

```json
{ "type": "presence", "user_id": "<uuid>", "username": "alice", "is_online": true, "online_users": [{...}] }
```

Read receipt (broadcast to the room after someone marks it read):

```json
{ "type": "read_receipt", "room_id": "<uuid>", "read_by": [{"user_id":"<uuid>","username":"bob","last_read_at":"..."}] }
```

Pong reply:

```json
{ "type": "pong", "text": "pong" }
```

Client-local error (e.g. invalid payload, unknown room, editing another's
message, editing a message that has already been edited once, reacting to a
missing message, or an invalid emoji):

```json
{ "type": "error", "message_id": "<uuid or nil>", "text": "room not found" }
```

## Project layout

```
cmd/server/             # entrypoint: config, DB, migrations, hub, server
cmd/client/             # line-based console client (chat)
cmd/admin/              # line-based console client (administration)
internal/
  config/               # env-based config
  models/               # User, Room, Message, WS envelope types
  store/
    migrations/         # embedded SQL migrations
    postgres/           # pgx pool + users/rooms/messages queries
  auth/                 # bcrypt + JWT + chi auth middleware
  hub/                  # in-memory client registry + room broadcast
  ws/                   # WebSocket upgrade + read/write pumps
  httpapi/              # REST handlers + chi router
  server/               # graceful HTTP server lifecycle
web/                    # React + TS PWA client (separate pnpm project)
Dockerfile              # multi-stage build (targets: runtime = Go API, caddy = TLS edge + PWA)
docker-compose.yml      # db + app + web (Caddy edge) + coturn (STUN/TURN) + livekit (SFU)
livekit.yaml            # LiveKit SFU config (API keys injected via env, never stored here)
Caddyfile               # HTTPS termination, API/WS proxy, PWA static serving
.dockerignore           # keeps secrets and generated files out of the build context
Makefile                # build/run/test/docker targets
```

## Future iterations

The data model and hub are intentionally shaped to accept later features without
rework:

- **Calls / group calls** — WebRTC offer/answer/ICE exchange carried over the
  same WebSocket, keyed by room.
- **Attachments** — add an `attachments` table + object storage.
- **Per-room membership** — the hub already broadcasts by room id; adding a
  membership table only changes *who* is in which room set.
- **Auto retention** — a scheduled job pruning messages older than N days is a
  straightforward addition; currently messages are kept forever.

## License

Licensed under the [MIT License](LICENSE).
