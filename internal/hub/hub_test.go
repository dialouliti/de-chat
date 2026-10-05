// Unit tests for hub.SendToUser: fan-out to every live connection of one
// user (the multi-tab case) and the delivered-connections count. The E2E
// call-signalling tests in internal/ws exercise the handler's use of the
// method; these pin the hub's own semantics, which a single socket cannot
// observe (the count is consumed internally by the ws handler).

package hub

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/models"
)

// fakeClient is a minimal hub.Client recording everything Send delivers.
type fakeClient struct {
	userID   uuid.UUID
	username string
	got      []models.ServerMsg
}

func (f *fakeClient) UserID() uuid.UUID {
	return f.userID
}

func (f *fakeClient) Username() string {
	return f.username
}

func (f *fakeClient) Send(m models.ServerMsg) {
	f.got = append(f.got, m)
}

// newTestHub runs a hub for the duration of the test. Register/Unregister and
// SendToUser are plain channel sends to the run loop: once a send returns,
// the run loop has the operation in hand and finishes it before servicing the
// next request, so sequential calls need no sleeps.
func newTestHub(t *testing.T) *Hub {
	t.Helper()
	h := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go h.Run(done)
	t.Cleanup(func() { close(done) })
	return h
}

func TestSendToUserFansOutToEveryConnection(t *testing.T) {
	h := newTestHub(t)

	uid := uuid.New()
	tabs := []*fakeClient{
		{userID: uid, username: "bob"},
		{userID: uid, username: "bob"},
		{userID: uid, username: "bob"},
	}
	outsider := &fakeClient{userID: uuid.New(), username: "alice"}

	for _, c := range tabs {
		h.Register(c)
	}
	h.Register(outsider)

	const wantContent = `{"type":"offer","sdp":"v=0"}`
	if n := h.SendToUser(uid, models.ServerMsg{Type: "call_offer", Content: wantContent}); n != len(tabs) {
		t.Fatalf("SendToUser delivered to %d connections, want %d (every tab, not just one)", n, len(tabs))
	}
	for i, c := range tabs {
		if len(c.got) != 1 {
			t.Fatalf("tab %d: received %d messages, want 1", i+1, len(c.got))
		}
		if c.got[0].Type != "call_offer" || c.got[0].Content != wantContent {
			t.Errorf("tab %d: got {%s %q}, want {call_offer %q}", i+1, c.got[0].Type, c.got[0].Content, wantContent)
		}
	}
	if len(outsider.got) != 0 {
		t.Errorf("unrelated user received %d messages, want 0", len(outsider.got))
	}
}

func TestSendToUserCountsOnlyLiveConnections(t *testing.T) {
	h := newTestHub(t)

	uid := uuid.New()
	tab1 := &fakeClient{userID: uid, username: "bob"}
	tab2 := &fakeClient{userID: uid, username: "bob"}
	h.Register(tab1)
	h.Register(tab2)

	// The user's connection set must shrink on unregister, or a still-online
	// user would be reported offline (or worse, leaked on a dead conn).
	h.Unregister(tab1)

	if n := h.SendToUser(uid, models.ServerMsg{Type: "call_ice"}); n != 1 {
		t.Fatalf("SendToUser delivered to %d connections after unregister, want 1", n)
	}
	if len(tab1.got) != 0 {
		t.Errorf("unregistered tab received %d messages, want 0", len(tab1.got))
	}
	if len(tab2.got) != 1 {
		t.Errorf("live tab received %d messages, want 1", len(tab2.got))
	}
}

func TestSendToUserOfflineReturnsZero(t *testing.T) {
	h := newTestHub(t)

	other := &fakeClient{userID: uuid.New(), username: "alice"}
	h.Register(other)

	if n := h.SendToUser(uuid.New(), models.ServerMsg{Type: "call_offer"}); n != 0 {
		t.Fatalf("SendToUser to an offline user returned %d, want 0", n)
	}
	if len(other.got) != 0 {
		t.Errorf("unrelated user received %d messages, want 0", len(other.got))
	}
}
