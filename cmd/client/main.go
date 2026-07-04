// Command dechat-client is a small line-based console client for deChat.
//
// It logs in with username + password, lets you pick a room, and then chats
// live over the server's WebSocket endpoint. Incoming messages are printed as
// they arrive. Commands inside a room: /back (return to room picker), /quit
// (exit).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pavelananev/dechat/internal/models"
)

const (
	defaultServerURL = "http://localhost:8080"
	historyLimit     = 50
	httpTimeout      = 15 * time.Second
	wsWriteWait      = 10 * time.Second
)

func main() {
	serverFlag := flag.String("server", "", "server URL (overrides DECHAT_URL and the default)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// serverURL: flag > env > default prompt.
	serverURL := *serverFlag
	if serverURL == "" {
		serverURL = os.Getenv("DECHAT_URL")
	}

	out := &outWriter{w: os.Stdout} // serialized stdout for clean interleaving
	in := bufio.NewReader(os.Stdin)

	// 1. Login -------------------------------------------------------------
	serverURL = promptDefault(in, out, serverURL, "Server URL", defaultServerURL)
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}

	var token string
	for {
		username := prompt(in, out, "Username")
		if username == "" {
			continue
		}
		password := promptSecret(in, out, "Password")
		if password == "" {
			continue
		}

		t, err := login(serverURL, username, password)
		if err != nil {
			out.println("  login failed: " + err.Error())
			continue
		}
		token = t
		out.println(fmt.Sprintf("  logged in as %s.", username))
		break
	}

	// Ctrl+C always exits the program cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. Room picker loop --------------------------------------------------
	for {
		select {
		case <-ctx.Done():
			out.println("bye")
			return
		default:
		}

		if back := roomPicker(ctx, in, out, serverURL, token, logger); back == pickerQuit {
			out.println("bye")
			return
		}
	}
}

// --- output helper --------------------------------------------------------

// outWriter serializes writes to stdout so the WS reader goroutine and the
// main goroutine never interleave mid-line.
type outWriter struct {
	w  *os.File
	mu sync.Mutex
}

func (o *outWriter) print(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprint(o.w, s)
}

func (o *outWriter) println(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintln(o.w, s)
}

// printInterrupting writes a message to stdout "between" user keystrokes by
// moving to the start of the line and adding a leading blank line, so the
// incoming message lands below the prompt; afterwards it re-emits the prompt.
func (o *outWriter) printInterrupting(s, prompt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// \r returns to column 0 so the prompt gets overwritten, the message
	// prints on its own line, then the prompt is re-shown.
	fmt.Fprintf(o.w, "\r\n%s\n%s", s, prompt)
}

// --- input helpers --------------------------------------------------------

// prompt reads one trimmed line from the user.
func prompt(in *bufio.Reader, out *outWriter, label string) string {
	out.print(label + ": ")
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

// promptDefault is like prompt but falls back to def when the line is empty.
func promptDefault(in *bufio.Reader, out *outWriter, current, label, def string) string {
	if current != "" {
		return current
	}
	out.print(fmt.Sprintf("%s [%s]: ", label, def))
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// promptSecret reads a line without echoing it. There is no portable pure-Go
// way to disable terminal echo without cgo, so we print nothing per keystroke
// and just read until Enter. The password is therefore invisible (not masked),
// which avoids the masked-input dependency cost.
func promptSecret(in *bufio.Reader, out *outWriter, label string) string {
	out.print(label + ": ")
	line, _ := in.ReadString('\n')
	out.print("\r" + strings.Repeat(" ", len(label)+2) + "\r") // clear the line
	return strings.TrimSpace(line)
}

// --- HTTP to the server ---------------------------------------------------

// loginResponse mirrors the server's login response (httpapi.loginResponse).
type loginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// login posts credentials and returns the JWT on success.
func login(serverURL, username, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("invalid username or password")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %s", resp.Status)
	}

	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", fmt.Errorf("bad login response: %w", err)
	}
	if lr.Token == "" {
		return "", fmt.Errorf("server returned an empty token")
	}
	return lr.Token, nil
}

// listRooms fetches the rooms the user can join.
func listRooms(ctx context.Context, serverURL, token string) ([]models.Room, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/v1/rooms", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	var rooms []models.Room
	if err := json.NewDecoder(resp.Body).Decode(&rooms); err != nil {
		return nil, fmt.Errorf("bad room list: %w", err)
	}
	return rooms, nil
}

// roomHistory fetches the most recent messages in a room.
func roomHistory(ctx context.Context, serverURL, token string, roomID uuid.UUID) ([]models.Message, error) {
	u := fmt.Sprintf("%s/api/v1/rooms/%s/messages?limit=%d", serverURL, roomID, historyLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	var msgs []models.Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("bad message history: %w", err)
	}
	return msgs, nil
}

// --- room picker ----------------------------------------------------------

const (
	pickerBack = iota
	pickerQuit
)

// roomPicker shows the room menu and, on selection, enters the room chat. It
// returns when the user leaves the room (to come back here) or quits.
func roomPicker(ctx context.Context, in *bufio.Reader, out *outWriter, serverURL, token string, log *slog.Logger) int {
	for {
		select {
		case <-ctx.Done():
			return pickerQuit
		default:
		}

		rooms, err := listRooms(ctx, serverURL, token)
		if err != nil {
			out.println("  could not load rooms: " + err.Error())
			out.print("\nPress Enter to retry, or q to quit: ")
			line, _ := in.ReadString('\n')
			if strings.TrimSpace(line) == "q" {
				return pickerQuit
			}
			continue
		}

		out.println("")
		out.println("Rooms:")
		if len(rooms) == 0 {
			out.println("  (no rooms yet — ask an admin to create one)")
		}
		for i, r := range rooms {
			out.println(fmt.Sprintf("  %d) %s", i+1, r.Name))
			if r.Description != "" {
				out.println("       " + r.Description)
			}
		}
		out.print(fmt.Sprintf("\nPick a room (1-%d, r=refresh, q=quit): ", len(rooms)))

		line, _ := in.ReadString('\n')
		choice := strings.TrimSpace(line)
		switch choice {
		case "q", "Q", "":
			if choice == "" {
				continue
			}
			return pickerQuit
		case "r", "R":
			continue
		}

		idx, ok := atoi(choice)
		if !ok || idx < 1 || idx > len(rooms) {
			out.println("  invalid choice")
			continue
		}

		enterRoom(ctx, in, out, serverURL, token, rooms[idx-1], log)
		return pickerBack // back to room picker (the outer loop re-enters here)
	}
}

// atoi parses a small positive integer. Returns false on non-numeric input.
func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// --- room chat ------------------------------------------------------------

// enterRoom prints history, opens the WS, and runs the send/receive loop. It
// returns when the user types /back or /quit, or when the WS drops.
func enterRoom(ctx context.Context, in *bufio.Reader, out *outWriter, serverURL, token string, room models.Room, log *slog.Logger) {
	// History first.
	msgs, err := roomHistory(ctx, serverURL, token, room.ID)
	if err != nil {
		out.println("  could not load history: " + err.Error())
	} else {
		out.println("")
		out.println(fmt.Sprintf("--- %s (history) ----------------------------------------", room.Name))
		for _, m := range reverseMessages(msgs) { // API is newest-first; print oldest-first
			out.println(formatMessage(m))
		}
	}
	out.println("--- live (type a message, /back, /quit) ------------------")

	// Open the WebSocket.
	wsURL, err := wsConnectURL(serverURL, token)
	if err != nil {
		out.println("  invalid server URL: " + err.Error())
		out.println("\nPress Enter to go back to rooms...")
		_, _ = in.ReadString('\n')
		return
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		out.println("  could not connect to chat: " + err.Error())
		out.println("\nPress Enter to go back to rooms...")
		_, _ = in.ReadString('\n')
		return
	}
	defer conn.Close()

	// readerDone is closed when the WS read loop ends (disconnect or close).
	readerDone := make(chan struct{})
	go wsReader(conn, out, room.ID, readerDone, log)

	const promptStr = "> "
	out.print(promptStr)

	// Input loop. We don't select on ctx here because a ReadString on stdin
	// blocks independently; Ctrl+C is caught by signal.NotifyContext above and
	// the program exits via the deferred conn.Close().
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			out.println("  input error: " + err.Error())
			return
		}
		text := strings.TrimRight(line, "\r\n")

		switch {
		case text == "/back":
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(wsWriteWait),
			)
			out.println("")
			return
		case text == "/quit":
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(wsWriteWait),
			)
			out.println("bye")
			os.Exit(0)
		case text == "":
			// ignore empty lines
		default:
			msg := models.ClientMsg{Type: "message", RoomID: room.ID, Content: text}
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteJSON(msg); err != nil {
				out.println("  send failed: " + err.Error())
				return
			}
		}
		out.print(promptStr)
		_ = readerDone // referenced to keep the channel in scope; disconnect is handled below
	}
}

// wsReader reads ServerMsgs until the connection closes. Only messages in the
// active room are displayed; everything else (other rooms' messages, errors
// for other contexts) is ignored. When the loop ends, readerDone is closed.
func wsReader(conn *websocket.Conn, out *outWriter, roomID uuid.UUID, done chan<- struct{}, log *slog.Logger) {
	defer close(done)
	for {
		var sm models.ServerMsg
		if err := conn.ReadJSON(&sm); err != nil {
			// Normal close (from /back or server shutdown) is not noisy.
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Debug("ws read ended", "error", err)
			}
			return
		}
		switch sm.Type {
		case "message":
			if sm.Message == nil || sm.Message.RoomID != roomID {
				continue // not for this room
			}
			out.printInterrupting(formatMessage(*sm.Message), "> ")
		case "error":
			if sm.Text != "" {
				out.printInterrupting("[error] "+sm.Text, "> ")
			}
			// "pong" is a keepalive; nothing to display.
		}
	}
}

// wsConnectURL builds the ws:// or wss:// URL for the chat endpoint with the
// JWT passed as a query parameter, as required by the server.
func wsConnectURL(serverURL, token string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already correct
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/ws"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// formatMessage renders one chat line: [HH:MM username] text.
func formatMessage(m models.Message) string {
	ts := m.CreatedAt.Local().Format("15:04")
	return fmt.Sprintf("[%s %s] %s", ts, m.Username, m.Content)
}

// reverseMessages returns a copy with the order reversed, for printing history
// oldest-first (the API returns newest-first).
func reverseMessages(msgs []models.Message) []models.Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]models.Message, len(msgs))
	for i, m := range msgs {
		out[len(msgs)-1-i] = m
	}
	return out
}
