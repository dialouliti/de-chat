package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// parseUUID parses a path parameter as a UUID, writing a 400 on failure.
// Returns false if the caller should abort.
func parseUUID(w http.ResponseWriter, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid id: expected a UUID")
		return uuid.Nil, false
	}
	return id, true
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation, so handlers can translate it into a 409 Conflict.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}
