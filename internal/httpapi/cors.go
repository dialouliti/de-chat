package httpapi

import (
	"net/http"
	"strings"
)

// corsOptions configures the CORS middleware. A CORSOrigin of "*" allows any
// origin (fine for a private app authenticated via a bearer token, where the
// browser cannot be tricked into sending a meaningful request without the
// token it holds in JS memory). When set to a specific origin (or
// comma-separated list), only that origin is permitted.
type corsOptions struct {
	allowOrigin string // "*", a single origin, or a comma-separated list
}

// allowedHeaders are the request headers the web client may send. Authorization
// is required for the bearer token; Content-Type for JSON bodies.
const allowedHeaders = "Authorization, Content-Type"

// allowedMethods covers every method the REST routes use.
const allowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

// cors returns a middleware that applies the CORS policy. It handles the
// preflight OPTIONS short-circuit and sets response headers on actual requests.
func cors(opts corsOptions) func(http.Handler) http.Handler {
	// Normalise the configured origin into a fast lookup if it's a list.
	var allowList map[string]struct{}
	wildcard := false
	if opts.allowOrigin == "*" || opts.allowOrigin == "" {
		wildcard = true
	} else if strings.Contains(opts.allowOrigin, ",") {
		allowList = make(map[string]struct{})
		for _, o := range strings.Split(opts.allowOrigin, ",") {
			allowList[strings.TrimSpace(o)] = struct{}{}
		}
	}

	resolve := func(reqOrigin string) string {
		if wildcard {
			return "*"
		}
		if allowList != nil {
			if _, ok := allowList[reqOrigin]; ok {
				return reqOrigin
			}
			return "" // not allowed → send no Access-Control-Allow-Origin
		}
		// Single configured origin: allow it only if the request matches.
		if reqOrigin == opts.allowOrigin {
			return opts.allowOrigin
		}
		return ""
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := resolve(origin)
			if allowed != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowed)
				// When the origin is specific (not "*"), echo it back along with
				// Vary so caches key on the request origin.
				if allowed != "*" {
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}

			// Preflight short-circuit. chi's middleware stack already passes
			// OPTIONS through to handlers; we handle it here instead so the
			// auth middleware doesn't reject a preflight lacking credentials.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
				w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
