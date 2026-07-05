# AGENTS.md

Project-specific guidance for ZCode agents working in this repo.
Read the **README.md** first for full API/architecture docs; this file captures
only things that aren't obvious from a glance at the code.

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
  `openapi.go` for any route change.
- **`internal/store/migrations/`** — new file pair, never edit existing.
- **Auth flow** — both REST and WS entry points share `Authenticate`; changes
  must be symmetric.
