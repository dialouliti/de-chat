# deChat

A small real-time chat **backend** for a private group, written in Go. It exposes a JSON REST API and a WebSocket endpoint for live message delivery. There is **no frontend** and **no open registration**: an administrator creates all user accounts and all rooms.

## Features (v1)

- Login with username + password → JWT
- Admin-managed users (create / list / delete)
- Admin-managed rooms (create / delete / clear)
- Real-time messages over WebSocket
- PostgreSQL-backed message history with cursor pagination
- Messages are kept indefinitely (admin can clear a room on demand)
- All authenticated users can see all rooms

Designed so that future **calls / group calls** (WebRTC signaling) can reuse the same room + hub model.

## Quick start (Docker Compose)

> Requires Docker. Start the Docker app first if the daemon isn't running.

```bash
cp .env.example .env
# Edit .env: set a strong JWT_SECRET. Leave ADMIN_PASSWORD empty to have a
# random one generated and logged on first startup.

make docker-up          # builds app + starts postgres, runs migrations, boots app
```

On first boot the logs print the bootstrap admin credentials (if `ADMIN_PASSWORD` was empty) — save them. The API is then available on `http://localhost:8080`.

Stop with `make docker-down`.

## Running locally (without Docker)

You need Go 1.21+ and a reachable PostgreSQL 16 instance.

```bash
# point the app at your DB
export DATABASE_URL="postgres://dechat:dechat@localhost:5432/dechat?sslmode=disable"
export JWT_SECRET="$(openssl rand -base64 32)"

make run
```

Migrations are applied automatically on startup.

## Configuration

All settings come from environment variables (see `.env.example`):

| Variable         | Default     | Notes                                              |
|------------------|-------------|----------------------------------------------------|
| `SERVER_ADDR`    | `:8080`     | Listen address                                     |
| `DATABASE_URL`   | *(none)*    | PostgreSQL connection string (required)            |
| `JWT_SECRET`     | *(none)*    | HMAC signing key (required; change in production)  |
| `JWT_TTL`        | `24h`       | Token lifetime                                     |
| `ADMIN_USERNAME` | `admin`     | Bootstrap admin username                           |
| `ADMIN_PASSWORD` | *(empty)*   | Bootstrap admin password; random if empty          |
| `LOG_LEVEL`      | `info`      | `debug`/`info`/`warn`/`error`                      |

## REST API

All endpoints are under `/api/v1`. Protected endpoints require
`Authorization: Bearer <jwt>`.

### Auth

| Method | Path              | Auth | Body / Notes                         |
|--------|-------------------|------|--------------------------------------|
| POST   | `/login`          | no   | `{username, password}` → `{token, username, role}` |
| GET    | `/me`             | yes  | Returns the caller's profile         |

### Rooms & messages (any user)

| Method | Path                              | Notes                                             |
|--------|-----------------------------------|---------------------------------------------------|
| GET    | `/rooms`                          | List all rooms                                    |
| GET    | `/rooms/{id}/messages`            | Message history (newest-first), paginated         |

`/rooms/{id}/messages` query params:

- `limit` — items per page (1–100, default 50)
- `before` — RFC 3339 timestamp cursor; returns messages older than this

### Admin only

| Method | Path                       | Body / Notes                                  |
|--------|----------------------------|-----------------------------------------------|
| POST   | `/rooms`                   | `{name, description}` → room                  |
| DELETE | `/rooms/{id}`              | Deletes room and its messages                 |
| POST   | `/rooms/{id}/clear`        | Deletes all messages, keeps the room          |
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

Or a lightweight ping:

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
    "id": "<uuid>",
    "room_id": "<uuid>",
    "user_id": "<uuid>",
    "username": "alice",
    "content": "hello",
    "created_at": "2026-07-04T12:00:00Z"
  }
}
```

Pong reply:

```json
{ "type": "pong", "text": "pong" }
```

Client-local error (e.g. invalid payload, unknown room):

```json
{ "type": "error", "message_id": "<uuid or nil>", "text": "room not found" }
```

## Console client

A small line-based console client (`cmd/client`) lets you log in, pick a room,
and chat live from the terminal. It reuses the server's protocol types, so it
adds no new dependencies.

```bash
make build-client        # produces ./bin/dechat-client
./bin/dechat-client      # prompts for server URL, username, password
```

You can also set the server URL up front and skip the prompt:

```bash
DECHAT_URL=http://localhost:8080 ./bin/dechat-client
# or
go run ./cmd/client -server http://localhost:8080
```

Flow:

1. **Login** — enter the server URL (Enter keeps `http://localhost:8080`),
   then username and password. On a bad password it re-prompts.
2. **Room picker** — lists rooms; type the number to enter one, `r` to
   refresh, `q` to quit.
3. **In a room** — type a message and press Enter to send. Incoming messages
   print as they arrive. Commands:
   - `/back` — leave the room and return to the room picker.
   - `/quit` — exit the client.
   - `Ctrl+C` — exit at any time.

Recent history (last 50 messages) is loaded when you enter a room. Because the
client is line-based, an incoming message may briefly overlap the line you are
typing; your input still sends normally when you press Enter.

## Admin client

A dedicated console client (`cmd/admin`) for administrators to manage rooms and
users from the terminal. It requires an admin account (verified server-side on
login; a non-admin account is rejected).

```bash
make build-admin         # produces ./bin/dechat-admin
./bin/dechat-admin       # prompts for server URL, username, password
```

Server URL can be preset with `DECHAT_URL` or the `-server` flag, like the chat
client.

After login it offers a main menu:

- **Rooms** — list, create, delete (also removes the room's messages), and
  clear messages.
- **Users** — list (blocked users are marked `[BLOCKED]`), create (with a
  `user`/`admin` role), delete, and **block / unblock**.

Blocking a user revokes their access immediately: they can no longer log in,
existing JWTs stop working, and their WebSocket connection is rejected on the
next reconnect.

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
Dockerfile              # multi-stage build
docker-compose.yml      # app + postgres
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
