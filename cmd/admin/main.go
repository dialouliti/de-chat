// Command dechat-admin is a console client for administering deChat: managing
// rooms (create / delete / clear) and users (create / delete / block / unblock).
//
// It logs in with an admin account (verified server-side) and drives the admin
// REST endpoints. It reuses the server's models and protocol types, so it adds
// no new dependencies beyond what the server already pulls in.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pavelananev/dechat/internal/models"
)

const (
	defaultServerURL = "http://localhost:8080"
	httpTimeout      = 15 * time.Second
)

func main() {
	serverFlag := flag.String("server", "", "server URL (overrides DECHAT_URL and the default)")
	flag.Parse()

	serverURL := *serverFlag
	if serverURL == "" {
		serverURL = os.Getenv("DECHAT_URL")
	}
	serverURL = strings.TrimSpace(serverURL)

	in := bufio.NewReader(os.Stdin)

	fmt.Println("deChat admin")

	// --- Login (admin only) ------------------------------------------------
	serverURL = promptDefault(in, serverURL, "Server URL", defaultServerURL)
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}

	var token, role string
	for {
		username := prompt(in, "Username")
		if username == "" {
			continue
		}
		password := prompt(in, "Password")
		if password == "" {
			continue
		}

		lr, err := login(serverURL, username, password)
		if err != nil {
			fmt.Println("  login failed: " + err.Error())
			continue
		}
		if lr.Role != string(models.RoleAdmin) {
			fmt.Printf("  this account (%q) is not an admin. Only admins can use this tool.\n", username)
			os.Exit(1)
		}
		token, role = lr.Token, lr.Role
		fmt.Printf("  logged in as %s (%s).\n\n", username, role)
		break
	}

	// --- Main menu ---------------------------------------------------------
	for {
		fmt.Println("Main menu:")
		fmt.Println("  1) Rooms")
		fmt.Println("  2) Users")
		fmt.Println("  q) Quit")
		choice := prompt(in, "Choose")
		switch choice {
		case "1":
			roomsMenu(in, serverURL, token)
		case "2":
			usersMenu(in, serverURL, token)
		case "q", "Q":
			fmt.Println("bye")
			return
		default:
			fmt.Println("  invalid choice")
		}
	}
}

// --- input helpers --------------------------------------------------------

func prompt(in *bufio.Reader, label string) string {
	fmt.Print(label + ": ")
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptDefault(in *bufio.Reader, current, label, def string) string {
	if current != "" {
		return current
	}
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// confirm returns true only when the user types y or yes.
func confirm(in *bufio.Reader, label string) bool {
	return strings.HasPrefix(strings.ToLower(prompt(in, label+" (y/n)")), "y")
}

// --- HTTP client ----------------------------------------------------------

// loginResponse mirrors the server's login response.
type loginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func login(serverURL, username, password string) (*loginResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	resp, err := httpPost(serverURL+"/api/v1/login", "", body)
	if err != nil {
		return nil, fmt.Errorf("cannot reach server: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var lr loginResponse
		if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
			return nil, fmt.Errorf("bad login response: %w", err)
		}
		return &lr, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("invalid credentials or account is blocked")
	default:
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
}

func listRooms(serverURL, token string) ([]models.Room, error) {
	resp, err := httpGet(serverURL+"/api/v1/rooms", token)
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

func listUsers(serverURL, token string) ([]models.User, error) {
	resp, err := httpGet(serverURL+"/api/v1/admin/users", token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	var users []models.User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("bad user list: %w", err)
	}
	return users, nil
}

// --- rooms menu -----------------------------------------------------------

func roomsMenu(in *bufio.Reader, serverURL, token string) {
	for {
		rooms, err := listRooms(serverURL, token)
		if err != nil {
			fmt.Println("  could not load rooms: " + err.Error())
			if !confirm(in, "Retry") {
				return
			}
			continue
		}

		fmt.Println("\nRooms:")
		for i, r := range rooms {
			line := fmt.Sprintf("  %d) %s", i+1, r.Name)
			if r.Description != "" {
				line += " — " + r.Description
			}
			fmt.Println(line)
		}
		fmt.Println("  n) new room")
		fmt.Println("  b) back    q) quit")
		choice := prompt(in, "Choose")

		switch choice {
		case "b", "B", "":
			if choice == "" {
				continue
			}
			return
		case "q", "Q":
			fmt.Println("bye")
			os.Exit(0)
		case "n", "N":
			createRoomFlow(in, serverURL, token)
			continue
		}

		idx, ok := atoi(choice)
		if !ok || idx < 1 || idx > len(rooms) {
			fmt.Println("  invalid choice")
			continue
		}
		roomActions(in, serverURL, token, rooms[idx-1])
	}
}

func createRoomFlow(in *bufio.Reader, serverURL, token string) {
	name := prompt(in, "Name")
	if name == "" {
		fmt.Println("  cancelled")
		return
	}
	description := prompt(in, "Description")

	body, _ := json.Marshal(map[string]string{"name": name, "description": description})
	resp, err := httpPost(serverURL+"/api/v1/rooms", token, body)
	if err != nil {
		fmt.Println("  request failed: " + err.Error())
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		fmt.Println("  room created.")
	case http.StatusConflict:
		fmt.Println("  a room with that name already exists.")
	default:
		fmt.Println("  failed: server returned " + resp.Status)
	}
}

func roomActions(in *bufio.Reader, serverURL, token string, room models.Room) {
	for {
		fmt.Printf("\nRoom: %s\n", room.Name)
		fmt.Println("  d) delete")
		fmt.Println("  c) clear messages")
		fmt.Println("  b) back")
		choice := prompt(in, "Choose")
		switch choice {
		case "d", "D":
			if !confirm(in, fmt.Sprintf("Delete room %q AND all its messages", room.Name)) {
				fmt.Println("  cancelled")
				continue
			}
			if err := adminDelete(serverURL, token, "/api/v1/rooms/"+room.ID.String()); err != nil {
				fmt.Println("  failed: " + err.Error())
				continue
			}
			fmt.Println("  room deleted.")
			return
		case "c", "C":
			if !confirm(in, fmt.Sprintf("Clear ALL messages in room %q", room.Name)) {
				fmt.Println("  cancelled")
				continue
			}
			if err := adminPost(serverURL, token, "/api/v1/rooms/"+room.ID.String()+"/clear"); err != nil {
				fmt.Println("  failed: " + err.Error())
				continue
			}
			fmt.Println("  messages cleared.")
		case "b", "B":
			return
		default:
			fmt.Println("  invalid choice")
		}
	}
}

// --- users menu -----------------------------------------------------------

func usersMenu(in *bufio.Reader, serverURL, token string) {
	for {
		users, err := listUsers(serverURL, token)
		if err != nil {
			fmt.Println("  could not load users: " + err.Error())
			if !confirm(in, "Retry") {
				return
			}
			continue
		}

		fmt.Println("\nUsers:")
		for i, u := range users {
			line := fmt.Sprintf("  %d) %-16s (%s)", i+1, u.Username, u.Role)
			if u.Disabled {
				line += "  [BLOCKED]"
			}
			fmt.Println(line)
		}
		fmt.Println("  n) new user")
		fmt.Println("  b) back    q) quit")
		choice := prompt(in, "Choose")

		switch choice {
		case "b", "B", "":
			if choice == "" {
				continue
			}
			return
		case "q", "Q":
			fmt.Println("bye")
			os.Exit(0)
		case "n", "N":
			createUserFlow(in, serverURL, token)
			continue
		}

		idx, ok := atoi(choice)
		if !ok || idx < 1 || idx > len(users) {
			fmt.Println("  invalid choice")
			continue
		}
		userActions(in, serverURL, token, users[idx-1])
	}
}

func createUserFlow(in *bufio.Reader, serverURL, token string) {
	username := prompt(in, "Username")
	if username == "" {
		fmt.Println("  cancelled")
		return
	}
	password := prompt(in, "Password")
	if password == "" {
		fmt.Println("  cancelled")
		return
	}
	role := promptDefault(in, "", "Role (user/admin)", "user")
	if role != "user" && role != "admin" {
		role = "user"
	}

	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
		"role":     role,
	})
	resp, err := httpPost(serverURL+"/api/v1/admin/users", token, body)
	if err != nil {
		fmt.Println("  request failed: " + err.Error())
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		fmt.Println("  user created.")
	case http.StatusConflict:
		fmt.Println("  a user with that name already exists.")
	default:
		fmt.Println("  failed: server returned " + resp.Status)
	}
}

func userActions(in *bufio.Reader, serverURL, token string, user models.User) {
	for {
		state := "active"
		if user.Disabled {
			state = "BLOCKED"
		}
		fmt.Printf("\nUser: %s (%s, %s)\n", user.Username, user.Role, state)
		fmt.Println("  d) delete")
		if user.Disabled {
			fmt.Println("  u) unblock")
		} else {
			fmt.Println("  k) block")
		}
		fmt.Println("  b) back")
		choice := prompt(in, "Choose")
		switch choice {
		case "d", "D":
			if !confirm(in, fmt.Sprintf("Delete user %q (also removes their messages)", user.Username)) {
				fmt.Println("  cancelled")
				continue
			}
			if err := adminDelete(serverURL, token, "/api/v1/admin/users/"+user.ID.String()); err != nil {
				fmt.Println("  failed: " + err.Error())
				continue
			}
			fmt.Println("  user deleted.")
			return
		case "k", "K":
			if !user.Disabled {
				if err := adminPost(serverURL, token, "/api/v1/admin/users/"+user.ID.String()+"/block"); err != nil {
					fmt.Println("  failed: " + err.Error())
					continue
				}
				user.Disabled = true
				fmt.Println("  user blocked.")
			}
		case "u", "U":
			if user.Disabled {
				if err := adminPost(serverURL, token, "/api/v1/admin/users/"+user.ID.String()+"/unblock"); err != nil {
					fmt.Println("  failed: " + err.Error())
					continue
				}
				user.Disabled = false
				fmt.Println("  user unblocked.")
			}
		case "b", "B":
			return
		default:
			fmt.Println("  invalid choice")
		}
	}
}

// --- low-level HTTP -------------------------------------------------------

func httpGet(url, token string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

func httpPost(url, token string, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

// adminDelete issues a DELETE and treats only non-2xx as an error.
func adminDelete(serverURL, token, path string) error {
	url := serverURL + path
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// adminPost issues an empty-body POST and treats only non-2xx as an error.
func adminPost(serverURL, token, path string) error {
	resp, err := httpPost(serverURL+path, token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// atoi parses a small positive integer.
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
