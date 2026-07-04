// Package httpapi wires the REST + WebSocket routes onto chi and provides
// request/response handlers.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// writeJSON serializes v as JSON and writes it with the given status. Any
// encoding error is reported as a 500.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Connection is likely broken; nothing useful to write now.
		return
	}
}

// jsonError writes a small JSON error object: {"error": "..."}.
func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON decodes r.Body into dst. It reports a 400 on malformed input.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		jsonError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
