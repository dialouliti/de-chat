package httpapi

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// messageListRequest builds a GET /rooms/{id}/messages request carrying the
// given query parameters, so parseMessageListQuery can be tested without a
// database or router.
func messageListRequest(t *testing.T, query url.Values) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, "/api/v1/rooms/00000000-0000-0000-0000-000000000000/messages", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.URL.RawQuery = query.Encode()
	return req
}

// TestParseMessageListQuery pins the query-parameter contract of
// GET /rooms/{id}/messages: limit defaults to 50 and silently ignores invalid
// values, before/after are exclusive RFC 3339 cursors, and combining the two
// cursors is rejected.
func TestParseMessageListQuery(t *testing.T) {
	const (
		beforeRaw = "2026-10-01T10:00:00Z"
		afterRaw  = "2026-10-12T09:30:00.5Z"
	)
	beforeWant, err := time.Parse(time.RFC3339Nano, beforeRaw)
	if err != nil {
		t.Fatalf("parse beforeWant: %v", err)
	}
	afterWant, err := time.Parse(time.RFC3339Nano, afterRaw)
	if err != nil {
		t.Fatalf("parse afterWant: %v", err)
	}

	tests := []struct {
		name       string
		query      url.Values
		wantLimit  int
		wantBefore bool // want beforeWant returned
		wantAfter  bool // want afterWant returned
		wantErr    bool
	}{
		{
			name:      "no cursors, default limit",
			query:     url.Values{},
			wantLimit: 50,
		},
		{
			name:      "valid after",
			query:     url.Values{"after": {afterRaw}},
			wantLimit: 50,
			wantAfter: true,
		},
		{
			name:       "valid before",
			query:      url.Values{"before": {beforeRaw}},
			wantLimit:  50,
			wantBefore: true,
		},
		{
			name:      "explicit limit",
			query:     url.Values{"limit": {"25"}},
			wantLimit: 25,
		},
		{
			name:      "invalid limit silently ignored",
			query:     url.Values{"limit": {"not-a-number"}},
			wantLimit: 50,
		},
		{
			name:    "before and after together",
			query:   url.Values{"before": {beforeRaw}, "after": {afterRaw}},
			wantErr: true,
		},
		{
			name:    "invalid before timestamp",
			query:   url.Values{"before": {"2026-10-01 not-a-timestamp"}},
			wantErr: true,
		},
		{
			name:    "invalid after timestamp",
			query:   url.Values{"after": {"yesterday"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, before, after, err := parseMessageListQuery(messageListRequest(t, tt.query))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMessageListQuery(%v) = no error, want error", tt.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMessageListQuery(%v) error: %v", tt.query, err)
			}

			if limit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", limit, tt.wantLimit)
			}
			if tt.wantBefore && !before.Equal(beforeWant) {
				t.Fatalf("before = %v, want %v", before, beforeWant)
			}
			if !tt.wantBefore && !before.IsZero() {
				t.Fatalf("before = %v, want zero time", before)
			}
			if tt.wantAfter && !after.Equal(afterWant) {
				t.Fatalf("after = %v, want %v", after, afterWant)
			}
			if !tt.wantAfter && !after.IsZero() {
				t.Fatalf("after = %v, want zero time", after)
			}
		})
	}
}

// TestParseMessageListQueryCursorsExclusive pins the exact error text for the
// mutually-exclusive-cursor rejection: the handler surfaces it verbatim as the
// 400 response message.
func TestParseMessageListQueryCursorsExclusive(t *testing.T) {
	_, _, _, err := parseMessageListQuery(messageListRequest(t, url.Values{
		"before": {"2026-10-01T10:00:00Z"},
		"after":  {"2026-10-12T09:30:00Z"},
	}))
	if err == nil {
		t.Fatal("combining 'before' and 'after' must be an error")
	}
	if want := "cannot combine 'before' and 'after'"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
