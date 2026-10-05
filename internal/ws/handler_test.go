// End-to-end tests for the voice-call signalling protocol (call_*): two WS
// clients talk through a real hub + WS handler mounted on httptest, backed by
// a fake MessageStore. Determinism rules: every read has a deadline, and the
// only synchronization primitive is a ping/pong round-trip after dialing —
// receiving the pong proves readPump started, which (by program order in
// ServeHTTP: Register → go writePump → go readPump) proves the hub has the
// client registered, so a SendToUser right after cannot race registration.

package ws

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pavelananev/dechat/internal/auth"
	"github.com/pavelananev/dechat/internal/hub"
	"github.com/pavelananev/dechat/internal/models"
	"github.com/pavelananev/dechat/internal/store/postgres"
)

const (
	// readDeadline bounds every expect-one-message read; the call flow is
	// sub-millisecond, so 2s leaves huge headroom for slow CI runners.
	readDeadline = 2 * time.Second
	// quietWindow bounds the negative ("nothing must arrive") reads. If an
	// unwanted message were coming, it would be queued synchronously by the
	// handler and written within milliseconds.
	quietWindow = 400 * time.Millisecond
)

// fakeStore implements MessageStore for the call-signalling tests. Only
// GetRoomByID matters for the call flow: it knows exactly one room and answers
// postgres.ErrNotFound for anything else. The remaining methods are never
// reached by the call_* handlers and return a loud error if that ever changes.
type fakeStore struct {
	room models.Room
}

var _ MessageStore = (*fakeStore)(nil)

func (f *fakeStore) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, username, content string) (models.Message, error) {
	return models.Message{}, errors.New("fakeStore: CreateMessage is not part of the call flow")
}

func (f *fakeStore) UpdateMessage(ctx context.Context, messageID, userID uuid.UUID, username, content string) (models.Message, error) {
	return models.Message{}, errors.New("fakeStore: UpdateMessage is not part of the call flow")
}

func (f *fakeStore) UpdateMessageAsAdmin(ctx context.Context, messageID uuid.UUID, content string) (models.Message, error) {
	return models.Message{}, errors.New("fakeStore: UpdateMessageAsAdmin is not part of the call flow")
}

func (f *fakeStore) ToggleReaction(ctx context.Context, messageID, roomID, userID uuid.UUID, emoji string) (bool, error) {
	return false, errors.New("fakeStore: ToggleReaction is not part of the call flow")
}

func (f *fakeStore) GetRoomByID(ctx context.Context, id uuid.UUID) (models.Room, error) {
	if id == f.room.ID {
		return f.room, nil
	}
	return models.Room{}, postgres.ErrNotFound
}

func (f *fakeStore) UpsertReadState(ctx context.Context, roomID, userID uuid.UUID) error {
	return errors.New("fakeStore: UpsertReadState is not part of the call flow")
}

func (f *fakeStore) ReadReceipts(ctx context.Context, roomID uuid.UUID) ([]models.ReadReceipt, error) {
	return nil, errors.New("fakeStore: ReadReceipts is not part of the call flow")
}

// --- WS test helpers ---

// dialWS opens a client connection to the test server with the given JWT.
func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// sendMsg writes one client message with a bounded write deadline.
func sendMsg(t *testing.T, conn *websocket.Conn, in models.ClientMsg) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(readDeadline))
	if err := conn.WriteJSON(in); err != nil {
		t.Fatalf("write %q: %v", in.Type, err)
	}
}

// readUntil reads server messages until one of typ arrives, skipping
// irrelevant types (presence fan-out from other connects, etc.). On deadline
// or read error it fatals, listing every type it skipped so the log shows what
// actually came back instead.
func readUntil(t *testing.T, conn *websocket.Conn, typ string) models.ServerMsg {
	t.Helper()
	var skipped []string
	_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
	for {
		var msg models.ServerMsg
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for %q: read failed: %v; skipped message types: %v", typ, err, skipped)
		}
		if msg.Type == typ {
			return msg
		}
		skipped = append(skipped, msg.Type)
	}
}

// expectNone asserts that no message of any of the given types arrives
// within window. It must be the last use of conn in a subtest: a read
// deadline that fires poisons the connection for further reads. A non-timeout
// read error is a real failure and is reported as such.
func expectNone(t *testing.T, conn *websocket.Conn, window time.Duration, types ...string) {
	t.Helper()
	unwanted := make(map[string]bool, len(types))
	for _, typ := range types {
		unwanted[typ] = true
	}
	var seen []string
	_ = conn.SetReadDeadline(time.Now().Add(window))
	for {
		var msg models.ServerMsg
		if err := conn.ReadJSON(&msg); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return // window elapsed, nothing unwanted arrived
			}
			t.Fatalf("expectNone(%v): read failed: %v; seen message types: %v", types, err, seen)
		}
		if unwanted[msg.Type] {
			t.Fatalf("expectNone(%v): %q arrived, but it must not have; seen message types: %v", types, msg.Type, seen)
		}
		seen = append(seen, msg.Type)
	}
}

// waitReady proves the connection is fully wired: the pong reply is produced
// by readPump, which only starts after the hub has registered the client.
func waitReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	sendMsg(t, conn, models.ClientMsg{Type: "ping"})
	readUntil(t, conn, "pong")
}

func TestVoiceCallSignaling(t *testing.T) {
	// Discard logs: the hub and handler log per-client debug noise.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	authSvc := auth.New("test-signalling-secret", time.Hour)
	aliceID := uuid.New()
	bobID := uuid.New()
	issue := func(userID uuid.UUID, username string) string {
		t.Helper()
		token, err := authSvc.IssueToken(userID, username, "user")
		if err != nil {
			t.Fatalf("issue token for %s: %v", username, err)
		}
		return token
	}
	aliceToken := issue(aliceID, "alice")
	bobToken := issue(bobID, "bob")

	h := hub.New(logger)
	hubDone := make(chan struct{})
	go h.Run(hubDone)
	t.Cleanup(func() { close(hubDone) })

	roomID := uuid.New()
	store := &fakeStore{room: models.Room{ID: roomID, Name: "general"}}

	srv := httptest.NewServer(New(h, store, authSvc, logger))
	t.Cleanup(srv.Close)

	// dialUser opens and readies a single connection for the given token.
	dialUser := func(t *testing.T, token string) *websocket.Conn {
		t.Helper()
		conn := dialWS(t, srv, token)
		waitReady(t, conn)
		return conn
	}

	// dialPair connects alice and bob on fresh sockets per subtest, so a
	// negative read that fires its deadline (poisoning the connection) never
	// affects the next scenario.
	dialPair := func(t *testing.T) (alice, bob *websocket.Conn) {
		t.Helper()
		return dialUser(t, aliceToken), dialUser(t, bobToken)
	}

	const offerSDP = `{"type":"offer","sdp":"v=0 o=- 4611731400430051336 2 IN IP4 127.0.0.1"}`
	const answerSDP = `{"type":"answer","sdp":"v=0 o=- 8310855874875459931 2 IN IP4 127.0.0.1"}`
	const iceAlice = `{"candidate":{"candidate":"candidate:1 1 UDP 2130706431 192.168.1.5 8998 typ host","sdpMid":"0","sdpMLineIndex":0}}`
	const iceBob = `{"candidate":{"candidate":"candidate:1 1 UDP 2130706431 192.168.1.7 9101 typ host","sdpMid":"0","sdpMLineIndex":0}}`

	t.Run("call_offer goes to the target only", func(t *testing.T) {
		alice, bob := dialPair(t)

		sendMsg(t, alice, models.ClientMsg{
			Type:         "call_offer",
			RoomID:       roomID,
			TargetUserID: bobID,
			Content:      offerSDP,
		})

		got := readUntil(t, bob, "call_offer")
		if got.UserID != aliceID {
			t.Errorf("offer user_id = %v, want %v (alice)", got.UserID, aliceID)
		}
		if got.Username != "alice" {
			t.Errorf("offer username = %q, want %q", got.Username, "alice")
		}
		if got.RoomID != roomID {
			t.Errorf("offer room_id = %v, want %v", got.RoomID, roomID)
		}
		if got.Content != offerSDP {
			t.Errorf("offer content = %q, want %q", got.Content, offerSDP)
		}

		// The offer is addressed, not broadcast: alice must not get her own
		// offer back within a short quiet window.
		expectNone(t, alice, quietWindow, "call_offer")
	})

	t.Run("call_answer goes back to the caller", func(t *testing.T) {
		alice, bob := dialPair(t)

		sendMsg(t, bob, models.ClientMsg{
			Type:         "call_answer",
			RoomID:       roomID,
			TargetUserID: aliceID,
			Content:      answerSDP,
		})

		got := readUntil(t, alice, "call_answer")
		if got.UserID != bobID {
			t.Errorf("answer user_id = %v, want %v (bob)", got.UserID, bobID)
		}
		if got.Username != "bob" {
			t.Errorf("answer username = %q, want %q", got.Username, "bob")
		}
		if got.RoomID != roomID {
			t.Errorf("answer room_id = %v, want %v", got.RoomID, roomID)
		}
		if got.Content != answerSDP {
			t.Errorf("answer content = %q, want %q", got.Content, answerSDP)
		}

		// Bob sent it; he must not receive it back either.
		expectNone(t, bob, quietWindow, "call_answer")
	})

	t.Run("call_ice flows in both directions", func(t *testing.T) {
		alice, bob := dialPair(t)

		sendMsg(t, alice, models.ClientMsg{
			Type:         "call_ice",
			RoomID:       roomID,
			TargetUserID: bobID,
			Content:      iceAlice,
		})
		sendMsg(t, bob, models.ClientMsg{
			Type:         "call_ice",
			RoomID:       roomID,
			TargetUserID: aliceID,
			Content:      iceBob,
		})

		toBob := readUntil(t, bob, "call_ice")
		if toBob.UserID != aliceID || toBob.Username != "alice" {
			t.Errorf("ice to bob: user = %q/%v, want alice/%v", toBob.Username, toBob.UserID, aliceID)
		}
		if toBob.RoomID != roomID {
			t.Errorf("ice to bob: room_id = %v, want %v", toBob.RoomID, roomID)
		}
		if toBob.Content != iceAlice {
			t.Errorf("ice to bob: content = %q, want %q", toBob.Content, iceAlice)
		}

		toAlice := readUntil(t, alice, "call_ice")
		if toAlice.UserID != bobID || toAlice.Username != "bob" {
			t.Errorf("ice to alice: user = %q/%v, want bob/%v", toAlice.Username, toAlice.UserID, bobID)
		}
		if toAlice.RoomID != roomID {
			t.Errorf("ice to alice: room_id = %v, want %v", toAlice.RoomID, roomID)
		}
		if toAlice.Content != iceBob {
			t.Errorf("ice to alice: content = %q, want %q", toAlice.Content, iceBob)
		}
	})

	t.Run("targeted delivery reaches every connection of the user", func(t *testing.T) {
		alice := dialUser(t, aliceToken)
		// Bob is online from two tabs — the documented reason SendToUser
		// iterates the user's whole connection set (hub.go: "a user may have
		// several tabs open"). A regression to single-connection delivery
		// must fail here.
		bobTab1 := dialUser(t, bobToken)
		bobTab2 := dialUser(t, bobToken)

		sendMsg(t, alice, models.ClientMsg{
			Type:         "call_offer",
			RoomID:       roomID,
			TargetUserID: bobID,
			Content:      offerSDP,
		})
		for i, conn := range []*websocket.Conn{bobTab1, bobTab2} {
			got := readUntil(t, conn, "call_offer")
			if got.UserID != aliceID || got.Username != "alice" {
				t.Errorf("offer on bob tab %d: user = %q/%v, want alice/%v", i+1, got.Username, got.UserID, aliceID)
			}
			if got.Content != offerSDP {
				t.Errorf("offer on bob tab %d: content = %q, want %q", i+1, got.Content, offerSDP)
			}
		}

		// Close one tab: bob is still online through the other, so an answer
		// must reach the live tab and must NOT be answered with an error.
		// This holds regardless of whether the hub has processed the closed
		// tab's Unregister yet: the live tab is in the set either way, so the
		// delivered-connections count stays >= 1 — no race, no sleep needed.
		_ = bobTab2.Close()
		sendMsg(t, alice, models.ClientMsg{
			Type:         "call_answer",
			RoomID:       roomID,
			TargetUserID: bobID,
			Content:      answerSDP,
		})
		got := readUntil(t, bobTab1, "call_answer")
		if got.UserID != aliceID || got.Content != answerSDP {
			t.Errorf("answer on bob tab 1: user_id = %v, content = %q; want %v / %q", got.UserID, got.Content, aliceID, answerSDP)
		}
		expectNone(t, alice, quietWindow, "error")
	})

	t.Run("call_join and call_leave are broadcast to the room", func(t *testing.T) {
		alice, bob := dialPair(t)

		for _, typ := range []string{"call_join", "call_leave"} {
			sendMsg(t, alice, models.ClientMsg{Type: typ, RoomID: roomID})

			// The sender receives its own broadcast event (same as typing).
			self := readUntil(t, alice, typ)
			other := readUntil(t, bob, typ)
			for name, msg := range map[string]models.ServerMsg{"alice": self, "bob": other} {
				if msg.UserID != aliceID {
					t.Errorf("%s seen by %s: user_id = %v, want %v (alice)", typ, name, msg.UserID, aliceID)
				}
				if msg.Username != "alice" {
					t.Errorf("%s seen by %s: username = %q, want %q", typ, name, msg.Username, "alice")
				}
				if msg.RoomID != roomID {
					t.Errorf("%s seen by %s: room_id = %v, want %v", typ, name, msg.RoomID, roomID)
				}
			}
		}
	})

	t.Run("call_offer and call_answer to an offline user return an error", func(t *testing.T) {
		alice, bob := dialPair(t)
		offline := uuid.New() // nobody is connected under this id

		for _, tc := range []struct {
			typ     string
			content string
		}{
			{"call_offer", offerSDP},
			{"call_answer", answerSDP},
		} {
			sendMsg(t, alice, models.ClientMsg{
				Type:         tc.typ,
				RoomID:       roomID,
				TargetUserID: offline,
				Content:      tc.content,
			})

			errMsg := readUntil(t, alice, "error")
			if errMsg.Text != "user is not online" {
				t.Fatalf("%s to offline user: error text = %q, want %q", tc.typ, errMsg.Text, "user is not online")
			}
		}

		// The offline "target" obviously gets nothing.
		expectNone(t, bob, quietWindow, "call_offer", "call_answer")
	})

	t.Run("call_ice to an offline user is dropped silently", func(t *testing.T) {
		alice, _ := dialPair(t)

		sendMsg(t, alice, models.ClientMsg{
			Type:         "call_ice",
			RoomID:       roomID,
			TargetUserID: uuid.New(),
			Content:      iceAlice,
		})

		// No error, no echo: ICE after hang-up is routine, so the server
		// must not spam the caller.
		expectNone(t, alice, quietWindow, "error")
	})

	t.Run("validation", func(t *testing.T) {
		alice, bob := dialPair(t)

		// The table mixes the targeted types (shared validation in
		// handleCallSignal) with the broadcast ones (broadcastCallEvent).
		cases := []struct {
			name string
			in   models.ClientMsg
			want string
		}{
			{
				name: "missing room_id, target_user_id and content",
				in:   models.ClientMsg{Type: "call_offer"},
				want: "room_id, target_user_id and content are required",
			},
			{
				name: "missing target_user_id and content",
				in:   models.ClientMsg{Type: "call_offer", RoomID: roomID},
				want: "room_id, target_user_id and content are required",
			},
			{
				name: "missing content",
				in:   models.ClientMsg{Type: "call_offer", RoomID: roomID, TargetUserID: bobID},
				want: "room_id, target_user_id and content are required",
			},
			{
				name: "unknown room",
				in:   models.ClientMsg{Type: "call_offer", RoomID: uuid.New(), TargetUserID: bobID, Content: offerSDP},
				want: "room not found",
			},
			{
				name: "target is the caller",
				in:   models.ClientMsg{Type: "call_offer", RoomID: roomID, TargetUserID: aliceID, Content: offerSDP},
				want: "cannot call yourself",
			},
			{
				name: "call_join without room_id",
				in:   models.ClientMsg{Type: "call_join"},
				want: "room_id is required",
			},
			{
				name: "call_join with unknown room",
				in:   models.ClientMsg{Type: "call_join", RoomID: uuid.New()},
				want: "room not found",
			},
			{
				name: "call_leave without room_id",
				in:   models.ClientMsg{Type: "call_leave"},
				want: "room_id is required",
			},
			{
				name: "call_leave with unknown room",
				in:   models.ClientMsg{Type: "call_leave", RoomID: uuid.New()},
				want: "room not found",
			},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				sendMsg(t, alice, tc.in)
				errMsg := readUntil(t, alice, "error")
				if errMsg.Text != tc.want {
					t.Fatalf("error text = %q, want %q", errMsg.Text, tc.want)
				}
			})
		}

		// None of the rejected payloads may have reached bob.
		expectNone(t, bob, quietWindow, "call_offer", "call_join", "call_leave")
	})
}
