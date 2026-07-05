package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"
)

// SpecServer renders the OpenAPI document in JSON or YAML and serves a small
// Swagger UI page. The document is built once and the serialized bytes are
// cached, since the spec never changes after startup.
type SpecServer struct {
	buildOnce sync.Once
	jsonBytes []byte
	yamlBytes []byte
	buildErr  error
}

// NewSpecServer returns a SpecServer that builds its document lazily on first
// use (so importing the package never pays the cost unless /openapi.* is hit).
func NewSpecServer() *SpecServer { return &SpecServer{} }

// ensureBuild renders the spec exactly once; subsequent calls are no-ops.
func (s *SpecServer) ensureBuild() error {
	s.buildOnce.Do(func() {
		doc, err := BuildSpec()
		if err != nil {
			s.buildErr = err
			return
		}
		if s.jsonBytes, err = json.MarshalIndent(doc, "", "  "); err != nil {
			s.buildErr = err
			return
		}
		// The kin types carry their own MarshalYAML, so yaml.v3 produces a
		// spec-compliant document without any custom marshalling.
		if s.yamlBytes, err = yaml.Marshal(doc); err != nil {
			s.buildErr = err
		}
	})
	return s.buildErr
}

// ServeJSON renders GET /openapi.json.
func (s *SpecServer) ServeJSON(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureBuild(); err != nil {
		http.Error(w, "failed to build spec: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Allow any client (browsers, Swagger UI, Postman) to read the spec.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(s.jsonBytes)
}

// ServeYAML renders GET /openapi.yaml.
func (s *SpecServer) ServeYAML(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureBuild(); err != nil {
		http.Error(w, "failed to build spec: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(s.yamlBytes)
}

// ServeUI renders GET /docs — a self-contained HTML page that loads Swagger
// UI from the jsDelivr CDN and points it at /openapi.json. The page itself is
// static; the spec stays the single source of truth.
func (s *SpecServer) ServeUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

// swaggerHTML is intentionally minimal: it loads Swagger UI from a CDN and
// configures it against the locally-served spec. No build step, no assets to
// embed. Requires outbound internet on the browser; /openapi.json itself
// works offline.
const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>deChat API docs</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
    <style>
      body { margin: 0; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
    <script>
      window.addEventListener("DOMContentLoaded", () => {
        window.ui = SwaggerUIBundle({
          url: "/openapi.json",
          dom_id: "#swagger-ui",
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis],
          layout: "BaseLayout",
        });
      });
    </script>
  </body>
</html>`
