# AGENTS.md

Project-specific guidance for ZCode agents working in this repo.
Read the **README.md** first for user-facing API/architecture docs; this file
captures the developer-facing map: structure, architecture, data model,
protocol, and the rules that matter for edits.

## What this is

A real-time chat **backend** for a private group: JSON REST API + WebSocket,
plus a React + TypeScript PWA client.

One Go module (`github.com/pavelananev/dechat`), four binaries:

- `cmd/server` — the API/WS server (`dechat`)
- `cmd/client` — line-based console chat client (`dechat-client`)
- `cmd/admin` — admin console client (`dechat-admin`)
- `cmd/openapi` — dumps the OpenAPI spec to disk (`dechat-openapi`)

And one separate Node project:

- `web/` — React + TypeScript PWA client (`dechat-web`), uses pnpm. Its typed
  API client is **generated** from the Go server's `openapi.json`. See
  `web/README.md` and `docs/CLIENT_NEXT_STEPS.md`.

## Commands

The Makefile sets `CGO_ENABLED=0` and assumes `GOTOOLCHAIN=local`.

```bash
make build          # server → bin/dechat
make build-client   # → bin/dechat-client
make build-admin    # → bin/dechat-admin
make run            # go run ./cmd/server (reads .env)
make docker-up      # full stack: app + postgres (the easiest way to run)
make docker-down
make openapi        # writes ./openapi.json + ./openapi.yaml
make vet            # go vet ./...
make test           # go test ./...
```

The web client (in `web/`, uses **pnpm**):

```bash
cd web
pnpm install        # first time; pnpm-workspace.yaml approves esbuild's build
pnpm gen:api        # regenerate src/api/schema.ts from ../openapi.json
pnpm gen:icons      # regenerate placeholder PWA icons (run once)
pnpm dev            # Vite dev server on :5173, proxies /api → :8080
pnpm typecheck      # tsc --noEmit
pnpm build          # production build → web/dist/
```

**Keep client and server in sync:** when the Go API changes, run
`make openapi` then `cd web && pnpm gen:api && pnpm typecheck`. The generated
`src/api/schema.ts` is gitignored. If the API contract changes, the server's
`internal/httpapi/openapi.go` (the spec builder) must be updated too.

When running any `go` command directly, set `GOTOOLCHAIN=local` — see the
gotcha below.

## Project layout

```
cmd/
  server/main.go            # entrypoint: config, DB, migrations, hub, server
  client/main.go            # line-based console chat client
  admin/main.go             # admin console client (users + rooms management)
  openapi/main.go           # dumps openapi.{json,yaml} to disk
internal/
  config/                   # env-based config
  models/                   # User, Room, Message, RoomMember, ReadReceipt,
                            # ReactionEntry, RoomUnread,
                            # WS envelope types (ClientMsg, ServerMsg, PresenceUser)
  auth/                     # JWT (HS256) + bcrypt; RequireAuth/RequireAdmin
  hub/                      # in-memory broadcast core + presence index
  ws/                       # WebSocket endpoint (upgrade, read/write pumps)
  store/
    migrations/             # embedded SQL migrations (go:embed *.sql)
    postgres/               # pgx/v5 pool: users, rooms, messages, read_state,
                            # room_members, reactions
  httpapi/                  # chi router, handlers, CORS, OpenAPI builder
  server/                   # http.Server wrapper with graceful shutdown
web/                        # React + TS PWA client (separate pnpm project)
  src/
    api/                    # openapi-fetch client + generated schema.ts
    store/auth.ts           # Zustand auth store (JWT in localStorage)
    store/unread.ts         # Zustand unread counters (title badge "(N) deChat")
    ws/                     # ChatSocket wrapper + useChatRoom hook
    components/             # Layout, MessageList, MessageInput, ProtectedRoute
    features/               # auth/, rooms/, chat/ page components
    lib/                    # format helpers, browser notifications
docs/
  CLIENT_NEXT_STEPS.md      # roadmap: push, calls, group calls, SFU
```

## Architecture rules that matter for edits

- **Layered, dependency flows inward.** `cmd/*` wires `internal/` packages;
  `internal/httpapi` (REST) and `internal/ws` (WebSocket) both sit above
  `internal/store/postgres`. `internal/hub` is the in-memory broadcast core.
  `internal/models` holds shared domain types — used by both server and
  clients, so don't import server-only packages there.
- **`internal/auth` must not import `internal/store`** (import cycle). The
  store-backed "is this user disabled?" check is injected via a
  `UserChecker` func set with `authSvc.SetUserChecker` in `cmd/server/main.go`.
  Same pattern (func/interface injection) for any new store dependency the
  auth layer needs.
- **Auth has two entry points that must agree.** REST requests go through
  `auth.RequireAuth` middleware; the WebSocket handshake authenticates via a
  `?token=` query param (browsers can't set headers on WS upgrade). Both call
  the shared `auth.Service.Authenticate`, which also enforces the per-request
  disabled-user check. **`GET /api/v1/ws` must stay OUTSIDE the `RequireAuth`
  route group** in `internal/httpapi/router.go` — putting it inside breaks the
  WS handshake (it expects a Bearer header the browser can't send).
- **Blocking is immediate.** A blocked user's JWT is rejected on the next
  request via the DB check inside `Authenticate`; deleted users are treated
  as disabled (stale tokens can't keep working). Don't change one entry point
  without the other.
- **Migrations are embedded and auto-applied at startup.**
  `internal/store/migrations/*.sql` are embedded via `go:embed`
  (`migrations.go`) and run by `postgres.go` on boot. Add a new
  `NNNN_name.{up,down}.sql` pair — never edit an applied migration in place.
- **OpenAPI spec is a parallel declaration.** `internal/httpapi/openapi.go`
  builds the spec from the actual Go structs via `openapi3gen`; every route
  in `router.go` has a matching operation there. **When you add or change a
  route, update `openapi.go` too** (flagged in a code comment). Run
  `make openapi` to regenerate the on-disk copy.
- **Room roles are moderation-only.** The `room_members` table records per-room
  ownership (`owner`/`member`) but does **not** gate visibility or posting —
  all authenticated users still see all rooms and can post. Ownership only
  grants delete/clear/member-management powers. Global admins override owners.
  The check is `canManageRoom(ctx, store, roomID, claims)` in
  `room_member_handlers.go`. Room deletion/clearing moved OUT of the
  admin-only route group into the authenticated group so non-admin owners can
  use them; ownership is enforced in the handler.
- **WebSocket protocol has several message types beyond `message`/`ping`.**
  `ClientMsg.Type` also includes `"typing"`, `"read"`, `"edit"`, `"reaction"`.
  `ServerMsg.Type` includes `"typing"`, `"presence"`, `"read_receipt"`,
  `"message_edited"`, `"reaction"`. Ephemeral events
  (typing/read/presence/edit/reaction) carry
  `user_id`/`username`/`room_id` at the envelope level so clients can filter
  by room without a nested `*Message`. **Presence is global** (no real
  room_id; delivered via a sentinel `uuid.Nil` room that the v1 broadcast-to-all
  rule fans out to everyone). Typing is **debounced server-side by state** —
  only rebroadcast on a real idle↔typing change, to avoid flooding the 256-deep
  outbox and triggering slow-consumer disconnects.
- **Hub has a user-id index for presence.** `hub.Hub` maintains
  `map[uuid.UUID]map[Client]struct{}` alongside the client set; `OnlineUsers()`
  returns a deduplicated snapshot. The `Client` interface requires
  `UserID()` + `Username()` + `Send()`. Presence is emitted by the WS handler
  on connect/disconnect, not by the hub — the hub stays a dumb relay.

## Key interfaces and injection points

- **`hub.Client`** (`internal/hub/hub.go`): `UserID() uuid.UUID`,
  `Username() string`, `Send(models.ServerMsg)`. Implemented by the
  **unexported `ws.client`**. `Send` is non-blocking: drops the client (async
  `close()`) if the 256-buffered outbox is full; recovers from
  send-on-closed-channel panics.
- **`ws.MessageStore`** (`internal/ws/handler.go`) — the store surface the WS
  layer depends on, decoupling `ws` from `postgres`:
  `CreateMessage`, `UpdateMessage`, `UpdateMessageAsAdmin`, `GetRoomByID`,
  `UpsertReadState`, `ReadReceipts`, `ToggleReaction`. Implemented by
  `*postgres.Store`; `ws.New` accepts this interface.
- **`auth.UserChecker`** (`internal/auth/middleware.go`):
  `func(ctx, userID uuid.UUID) (disabled bool, err error)`. Injected via
  `Service.SetUserChecker`; default `noOpChecker` (nobody disabled). In
  `cmd/server/main.go` it is wired to `store.GetUserByID` → returns
  `u.Disabled`. This is the **block-takes-immediate-effect** mechanism:
  `Authenticate` (used by both REST `RequireAuth` and the WS handshake)
  consults the checker on every request.
- **`httpapi.Deps`** — struct-based DI bundling `*postgres.Store`,
  `*auth.Service`, `*ws.Handler`, `CORSOrigin string`. Handlers use finer-
  grained per-group dep structs (`AuthDeps`, `RoomDeps`, `AdminDeps`,
  `RoomMemberDeps`), each embedding just the store (and AuthDeps adds
  `*auth.Service`).
- **Ownership/permission** is enforced in-handler via
  `canManageRoom(ctx, store, roomID, claims)` (admin or room owner) rather
  than via middleware.

## Database schema

Six tables across migrations 0001–0005. `CREATE EXTENSION pgcrypto` is in
0001 for `gen_random_uuid()`. FK rules worth noting: `messages` rows cascade
on room/user delete; `rooms.created_by` is **ON DELETE RESTRICT** (a user who
created rooms can't be deleted until those rooms are removed).

| Table | Key columns | Notes |
|---|---|---|
| `users` | `id` uuid PK, `username` UNIQUE, `password_hash`, `role` CHECK('admin','user'), `disabled` bool, timestamps | accounts are admin-created; no self-registration |
| `rooms` | `id` uuid PK, `name` UNIQUE, `description`, `created_by` → users (RESTRICT), `created_at` | |
| `messages` | `id` uuid PK, `room_id` → rooms (CASCADE), `user_id` → users (CASCADE), `content`, `created_at`, `edited_at` (nullable) | index `(room_id, created_at DESC)` for newest-first pagination |
| `message_read_state` | PK `(room_id, user_id)`, `last_read_at` | per-room read cursor; both FKs CASCADE |
| `room_members` | PK `(room_id, user_id)`, `role` CHECK('owner','member'), `joined_at` | moderation-only; both FKs CASCADE. 0004 backfills existing rooms' creators as owners. |
| `message_reactions` | PK `(message_id, user_id, emoji)`, `created_at` | one emoji reaction per (message, user, emoji); re-sending the same triple toggles it off (store's insert-or-delete); both FKs CASCADE. 0005. |

Sentinel errors: `postgres.ErrNotFound` (→ 404), `postgres.ErrForbidden`
(→ 403, used by `UpdateMessage` when a non-author edits), `postgres.ErrAlreadyEdited`
(returned by both edit paths when a message has already been edited once —
each message is editable a single time; enforced in SQL via `edited_at IS NULL`),
`postgres.ErrInvalidEmoji` (returned wrapped by `ToggleReaction` when the emoji
is empty, longer than 32 bytes, or contains ASCII control characters →
client-local WS `error` "invalid emoji").

## REST routes

Global middleware: `RequestID`, `RealIP`, `Recoverer`, then `cors(...)` (runs
**before** auth so preflight OPTIONS succeeds). All API routes under
`/api/v1`.

```
POST   /login                                    (public)              Login
GET    /ws                                       (public; ?token= auth) WS.ServeHTTP

  [ RequireAuth ]
  GET    /me
  GET    /me/unread
  GET    /call/config                                                   CallConfig
  GET    /rooms
  GET    /rooms/{id}/messages   ?limit&before&after
  DELETE /api/v1/rooms/{id}                     (owner-or-admin)       DeleteRoom
  POST   /api/v1/rooms/{id}/clear               (owner-or-admin)       ClearRoom
  GET    /rooms/{id}/members                                            ListMembers
  POST   /rooms/{id}/members                    (owner-or-admin)       AddMember
  DELETE /rooms/{id}/members/{userId}           (owner-or-admin)       RemoveMember

      [ RequireAuth + RequireAdmin ]
      POST   /rooms                                                    CreateRoom
      POST   /admin/users                                              CreateUser
      GET    /admin/users                                              ListUsers
      DELETE /admin/users/{id}                                         DeleteUser
      POST   /admin/users/{id}/block                                   BlockUser
      POST   /admin/users/{id}/unblock                                 UnblockUser

GET    /healthz  /openapi.json  /openapi.yaml  /docs  (public)
```

Pagination: `GET /rooms/{id}/messages` takes `limit` (1–100, default 50) and
a single RFC 3339 timestamp cursor, exclusive: `before` returns messages older
than it, **newest-first**; `after` returns messages newer than it,
**oldest-first** (ascending — clients append a backfill page after a WS
reconnect using the room's high-water mark). The two cursors are mutually
exclusive (400). Each message carries an aggregated `reactions` array
(`[{emoji, user_id, username}]`, ordered by reaction `created_at` ascending;
the field is absent when a message has none — live updates arrive as WS
`"reaction"` events instead).

Unread counts: `GET /me/unread` returns `[{room_id, unread_count}]` — only
other users' messages newer than the caller's `message_read_state` cursor
(all of them when no cursor exists). Rooms with zero unread are omitted, so
the response is `[]`, never `null`.

Call config: `GET /call/config` returns `{ttl_seconds, ice_servers}` for
WebRTC — a STUN entry always (from `STUN_URLS`) plus a TURN entry when
`TURN_URLS` is set, carrying short-lived credentials in coturn's REST API
scheme (`username = "<unix-expiry>:<caller>"`, `credential =
base64(HMAC-SHA1(TURN_SECRET, username))`), regenerated per request in
`call_handlers.go`. Config lives in `config.ICEConfig` and reaches the
handler through `httpapi.Deps.ICE`.

## WebSocket protocol

Connect: `GET /api/v1/ws?token=<jwt>` (JWT as query param; browser WS can't set
headers). Server pings every ~54s (expects pong within 60s). Messages are
JSON envelopes keyed on `type`. **The server broadcasts to ALL connected
clients; the client filters by `room_id` locally.** Presence is global.

**Client → server** (`ClientMsg`):

| `type` | Fields | Server action |
|---|---|---|
| `message` | `room_id`, `content` | persist + broadcast `"message"` |
| `edit` | `message_id`, `room_id`, `content` | author-or-admin edit (one-shot only — a second edit returns `error` "this message has already been edited") → `"message_edited"` |
| `reaction` | `message_id`, `room_id`, `emoji` | toggle the caller's reaction (insert if new, delete if the same triple exists) → broadcast `"reaction"` with `reaction_added` true/false |
| `typing` | `room_id`, `is_typing` | state-debounced broadcast `"typing"` |
| `read` | `room_id` | upsert read cursor → broadcast `"read_receipt"` |
| `ping` | — | reply `"pong"` (client-local) |

**Server → client** (`ServerMsg`):

| `type` | Fields |
|---|---|
| `message` | `message` (full row incl. `edited_at`) |
| `message_edited` | `message`, `message_id` (the payload does **not** carry `reactions` — clients keep locally known ones) |
| `reaction` | `room_id`, `message_id`, `user_id`, `username`, `emoji`, `reaction_added` (true = inserted, false = removed) |
| `typing` | `room_id`, `user_id`, `username`, `is_typing` |
| `presence` | `user_id`, `username`, `is_online`, `online_users` (snapshot on connect) |
| `read_receipt` | `room_id`, `read_by` (array of `{user_id, username, last_read_at}`) |
| `pong` | `text` |
| `error` | `message_id`, `text` (client-local, not broadcast) |

The envelope carries `room_id`/`user_id`/`username` at top level on ephemeral
events so clients can filter by room without a nested `*Message`.

## Config

All settings from env vars (see `.env.example`):

| Variable | Default | Notes |
|---|---|---|
| `SERVER_ADDR` | `:8080` | listen address |
| `DATABASE_URL` | *(none)* | PostgreSQL connection string (**required**) |
| `JWT_SECRET` | *(none)* | HMAC signing key (**required**, min 32 bytes; server refuses to boot on a weak/default value) |
| `JWT_TTL` | `24h` | token lifetime (`time.ParseDuration`) |
| `ADMIN_USERNAME` | `admin` | bootstrap admin username |
| `ADMIN_PASSWORD` | *(empty)* | empty → random password generated + logged once |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `CORS_ORIGIN` | `*` | `*`, single origin, or comma-separated list |

Web client: `VITE_API_URL` (default `http://localhost:8080`; leave empty in dev
— Vite proxies `/api` to the backend).

## Conventions

- **Go 1.21, pinned deps.** Dependency versions are deliberately held back
  (see gotcha below). Don't `go get` latest without checking the version's
  `go.mod` directive — newer releases of `kin-openapi`, `pgx`, `crypto` etc.
  demand Go 1.23+/1.25+ and will break this build.
- **JSON via struct tags.** Models and request/response structs use `json`
  tags; `PasswordHash` is `json:"-"` and must never be serialized.
- **Structured logging** with `log/slog`; handlers receive a logger via deps,
  not a global.
- **Errors → `jsonError(w, status, msg)`** in HTTP handlers; Postgres
  unique-violation → 409 via `isUniqueViolation` in `helpers.go`.
- **UUID path params** parsed with `parseUUID` (writes 400 on failure).

## Gotchas

- **Go toolchain pinning.** `go.mod` says `go 1.21` and several deps
  (`kin-openapi`, `golang.org/x/crypto`) are held at versions whose `go.mod`
  also targets 1.21. Newer versions require Go 1.23–1.25 and will trigger an
  automatic toolchain download. Always run go commands with
  `GOTOOLCHAIN=local` (the Makefile does this implicitly via its environment;
  for direct `go` invocations, prefix it yourself). Never bump the
  `go 1.21` directive in `go.mod` without explicit intent.
- **`kin-openapi` v0.124.0 specifically** — it's the newest tag whose
  `go.mod` requires exactly `go 1.21`. Its `openapi3gen` does not understand
  `uuid.UUID` (leaves such fields as empty `{}`); `openapi.go` patches them
  via `patchUUIDFields`. Its `doc.Validate` rejects any `$ref` that resolves
  via `components` rather than inlining, so the builder uses a custom
  `checkRefs` walker instead.
- **CGO off.** `CGO_ENABLED=0` is required (no C toolchain assumed; matches
  the Dockerfile). The Makefile exports it.
- **`.env` is gitignored** and holds secrets — never print or commit it.
  `.env.example` is the template.
- **`bin/`, `openapi.json`, `openapi.yaml` are generated** and gitignored.
  Don't commit them. `web/src/api/schema.ts` and `web/dist/` are also
  generated — never hand-edit the schema file.
- **CORS is required for any browser client.** The backend applies the
  `CORS_ORIGIN` env (default `*`) via middleware in `internal/httpapi/cors.go`,
  wired before the auth middleware so preflight OPTIONS requests aren't
  rejected. If you add a new browser-facing route, it inherits CORS
  automatically; if you change auth, keep CORS ahead of it.
- **Web client build quirk (pnpm + esbuild).** pnpm v11 blocks esbuild's
  postinstall by default; `web/pnpm-workspace.yaml` allows it. If
  `pnpm build` fails with esbuild missing, run `pnpm rebuild esbuild` or
  `pnpm add -D @esbuild/<platform>` directly.

## Before editing sensitive areas

- **`internal/auth`** — keep `UserChecker` injection; don't introduce a store
  import.
- **`internal/httpapi/router.go`** — keep `/ws` outside `RequireAuth`; update
  `openapi.go` for any route change. Room delete/clear/member routes live in
  the **authenticated** group (not admin-only) — ownership is enforced by
  `canManageRoom` inside the handlers.
- **`internal/ws/handler.go`** — the `Client` interface now needs
  `Username()`; adding new WS message types means extending both
  `models.ClientMsg`/`ServerMsg` and the `handle` switch, plus the web
  client's `ServerMessage` union in `socket.ts` and `handleServerMessage`.
- **`internal/store/migrations/`** — new file pair, never edit existing.
- **Auth flow** — both REST and WS entry points share `Authenticate`; changes
  must be symmetric.
