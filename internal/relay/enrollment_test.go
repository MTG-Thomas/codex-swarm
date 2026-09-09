package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const enrollmentID = "01a086c2-c1e3-7b51-9d1a-0934e8e8c309"

func TestAutoEnrollmentPrivacyReplayAndRevocation(t *testing.T) {
	for _, mode := range []string{"new", "existing", "local-revoked", "central-revoked", "policy-off", "wrong-host", "wrong-user", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			gets, posts := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Swarm-Host") != "test-host" {
					t.Error("host missing")
				}
				if r.Method == "GET" {
					gets++
					enabled := 1
					if mode == "central-revoked" {
						enabled = 0
					}
					if mode == "pagination" && gets == 1 {
						json.NewEncoder(w).Encode(map[string]any{"tasks": []any{}, "next_cursor": "00000000-0000-0000-0000-000000000001"})
						return
					}
					if mode == "existing" || mode == "central-revoked" || mode == "pagination" {
						json.NewEncoder(w).Encode(map[string]any{"tasks": []any{map[string]any{"host_id": "test-host", "thread_id": enrollmentID, "title": "Retain original", "enabled": enabled}}})
						return
					}
					io.WriteString(w, `{"tasks":[]}`)
					return
				}
				posts++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if len(b) != 3 || b["thread_id"] != enrollmentID || b["title"] != "Codex task "+enrollmentID || b["enabled"] != true {
					t.Errorf("unexpected metadata: %v", b)
				}
				json.NewEncoder(w).Encode(map[string]any{"host_id": "test-host", "thread_id": enrollmentID, "enabled": true})
			}))
			defer srv.Close()
			j, err := OpenJournal(filepath.Join(t.TempDir(), "relay.db"), srv.URL, "test-host")
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if mode == "local-revoked" {
				j.Enroll(t.Context(), enrollmentID, false)
			}
			p := EnrollmentPolicy{Enabled: true, Host: "test-host", OSUser: "current"}
			if mode == "policy-off" {
				p.Enabled = false
			}
			if mode == "wrong-host" {
				p.Host = "other"
			}
			if mode == "wrong-user" {
				p.OSUser = "other"
			}
			e, err := DecodeSessionStart(strings.NewReader(`{"session_id":"` + enrollmentID + `","hook_event_name":"SessionStart","source":"resume","cwd":"private/customer","transcript_path":"secret/path","prompt":"secret payload"}`))
			if err != nil {
				t.Fatal(err)
			}
			state, err := AutoEnroll(t.Context(), &Client{URL: srv.URL, Host: "test-host", Token: strings.Repeat("a", 32)}, j, p, "current", e)
			switch mode {
			case "wrong-host", "wrong-user":
				if err == nil || gets+posts != 0 {
					t.Fatalf("policy bypass: %s %v", state, err)
				}
			case "policy-off":
				if state != "disabled" || gets+posts != 0 {
					t.Fatal(state, err)
				}
			case "local-revoked":
				if state != "revoked" || gets+posts != 0 {
					t.Fatal(state, err)
				}
			case "central-revoked":
				if state != "revoked" || posts != 0 || j.allowed(t.Context(), enrollmentID) {
					t.Fatal(state, err)
				}
			default:
				if err != nil || state != "enrolled" || !j.allowed(t.Context(), enrollmentID) {
					t.Fatal(state, err)
				}
				if mode == "new" && posts != 1 {
					t.Fatal(posts)
				}
				if mode != "new" && posts != 0 {
					t.Fatal(posts)
				}
				if mode == "pagination" && gets != 2 {
					t.Fatal(gets)
				}
			}
		})
	}
}

func TestAutoEnrollmentFailureDoesNotAuthorizeLocalDelivery(t *testing.T) {
	for _, response := range []string{`{"host_id":"other","thread_id":"` + enrollmentID + `","enabled":true}`, `{"host_id":"test-host","thread_id":"` + enrollmentID + `","enabled":false}`, `malformed`} {
		t.Run(response, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					io.WriteString(w, `{"tasks":[]}`)
				} else {
					io.WriteString(w, response)
				}
			}))
			defer srv.Close()
			j, err := OpenJournal(filepath.Join(t.TempDir(), "relay.db"), srv.URL, "test-host")
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			_, err = AutoEnroll(context.Background(), &Client{URL: srv.URL, Host: "test-host", Token: strings.Repeat("a", 32)}, j, EnrollmentPolicy{Enabled: true, Host: "test-host", OSUser: "uid"}, "uid", SessionStart{SessionID: enrollmentID, Event: "SessionStart", Source: "startup"})
			if err == nil || j.allowed(t.Context(), enrollmentID) {
				t.Fatal("failed enrollment allowed execution")
			}
		})
	}
}

func TestSessionStartInputBounds(t *testing.T) {
	for _, s := range []string{`{}`, `{"session_id":"` + enrollmentID + `","hook_event_name":"SubagentStart","source":"startup"}`, `{"session_id":"` + enrollmentID + `","hook_event_name":"SessionStart","source":"compact"}`, strings.Repeat(" ", 65537), `{} {}`} {
		if _, err := DecodeSessionStart(strings.NewReader(s)); err == nil {
			t.Fatal("accepted invalid hook")
		}
	}
}
