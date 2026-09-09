package main

import (
	"bytes"
	"encoding/json"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStartFailureWarnsWithoutBlockingOrLeaking(t *testing.T) {
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}}
	err := c.relaySessionStart([]string{"--config", "/missing/private-token-config", "--policy", "/missing/policy"}, strings.NewReader(`{"secret":"NEVER-LOG-ME"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["systemMessage"] == nil || strings.Contains(out.String(), "NEVER-LOG-ME") || strings.Contains(out.String(), "private-token-config") {
		t.Fatal(out.String())
	}
	if result["continue"] == false {
		t.Fatal("blocked task")
	}
}

func TestSessionStartCommandEnrollsOnlyCurrentTask(t *testing.T) {
	if relay.CheckUser() != nil {
		t.Skip("requires non-root user")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	const id = "01a086c2-c1e3-7b51-9d1a-0934e8e8c309"
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`{"tasks":[]}`))
			return
		}
		posts++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 3 || body["thread_id"] != id {
			t.Error(body)
		}
		json.NewEncoder(w).Encode(map[string]any{"host_id": "test-host", "thread_id": id, "enabled": true})
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "relay.json")
	policy := filepath.Join(dir, "policy.json")
	b, _ := json.Marshal(map[string]string{"url": srv.URL, "host": "test-host", "token": strings.Repeat("a", 32), "journal": filepath.Join(dir, "relay.db")})
	if err = os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(relay.EnrollmentPolicy{Enabled: true, Host: "test-host", OSUser: u.Uid})
	if err = os.WriteFile(policy, b, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}}
	err = c.relaySessionStart([]string{"--config", cfg, "--policy", policy}, strings.NewReader(`{"session_id":"`+id+`","source":"startup","hook_event_name":"SessionStart","transcript_path":"do-not-read","cwd":"private"}`))
	if err != nil || posts != 1 || !strings.Contains(out.String(), "hookSpecificOutput") {
		t.Fatal(err, posts, out.String())
	}
}
