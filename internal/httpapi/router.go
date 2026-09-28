package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/store/postgres"
	"github.com/pavelananev/dechat/internal/ws"
)

// Deps bundles everything needed to build the HTTP router.
type Deps struct {
	Store *postgres.Store
	Auth  *auth.Service
	WS    *ws.Handler
	// CORSOrigin controls the Access-Control-Allow-Origin response header.
	// "*", a single origin, or a comma-separated list. Empty defaults to "*".
	CORSOrigin string
}

// New builds the top-level http.Handler with all routes mounted under
// /api/v1.
func New(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	// CORS must run before the auth middleware so preflight OPTIONS requests
	// (which carry no credentials) are answered without a 401.
	r.Use(cors(corsOptions{allowOrigin: d.CORSOrigin}))
	// No request body size limiting for now; v1 messages are text-only and
	// already capped at the WS layer. Easy to add chimw.RequestSize later.

	r.Route("/api/v1", func(r chi.Router) {
		// Public.
		r.Post("/login", AuthDeps{Store: d.Store, Auth: d.Auth}.Login)

		// WebSocket does its own auth via the ?token= query parameter, since
		// browsers cannot set custom headers on the WS handshake. It must sit
		// outside RequireAuth, which expects an Authorization header.
		r.Get("/ws", d.WS.ServeHTTP)

		// Authenticated.
		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireAuth)

			r.Get("/me", AuthDeps{Store: d.Store, Auth: d.Auth}.Me)
			r.Get("/me/unread", AuthDeps{Store: d.Store, Auth: d.Auth}.MeUnread)

			// Room list + message history are available to any user.
			r.Get("/rooms", RoomDeps{Store: d.Store}.ListRooms)
			r.Get("/rooms/{id}/messages", RoomDeps{Store: d.Store}.ListMessages)

			// Room deletion / clearing are gated by ownership inside the
			// handler (owner OR global admin), so they sit in the authenticated
			// group rather than the admin-only group.
			r.Delete("/rooms/{id}", AdminDeps{Store: d.Store}.DeleteRoom)
			r.Post("/rooms/{id}/clear", AdminDeps{Store: d.Store}.ClearRoom)

			// Room membership: list is open to any authenticated user; add /
			// remove are owner-or-admin (checked in the handler).
			r.Get("/rooms/{id}/members", RoomMemberDeps{Store: d.Store}.ListMembers)
			r.Post("/rooms/{id}/members", RoomMemberDeps{Store: d.Store}.AddMember)
			r.Delete("/rooms/{id}/members/{userId}", RoomMemberDeps{Store: d.Store}.RemoveMember)

			// Admin-only: room creation + user administration.
			r.Group(func(r chi.Router) {
				r.Use(d.Auth.RequireAdmin)
				r.Post("/rooms", AdminDeps{Store: d.Store}.CreateRoom)

				// User administration.
				r.Route("/admin/users", func(r chi.Router) {
					r.Post("/", AdminDeps{Store: d.Store}.CreateUser)
					r.Get("/", AdminDeps{Store: d.Store}.ListUsers)
					r.Delete("/{id}", AdminDeps{Store: d.Store}.DeleteUser)
					r.Post("/{id}/block", AdminDeps{Store: d.Store}.BlockUser)
					r.Post("/{id}/unblock", AdminDeps{Store: d.Store}.UnblockUser)
				})
			})
		})
	})

	// Health check for docker-compose / orchestrators.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// OpenAPI spec + Swagger UI. Public (no auth): the spec advertises the
	// auth scheme, and clients need to read it before they have a token. The
	// SpecServer builds the document lazily on first request and caches it.
	spec := NewSpecServer()
	r.Get("/openapi.json", spec.ServeJSON)
	r.Get("/openapi.yaml", spec.ServeYAML)
	r.Get("/docs", spec.ServeUI)
	// Convenience aliases so /docs/ also works when typed into a browser.
	r.Get("/docs/", spec.ServeUI)

	return r
}
