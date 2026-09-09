package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayTasksPaginates(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cursor := r.URL.Query().Get("after")
		next := "00000000-0000-4000-8000-000000000001"
		if calls == 2 {
			if cursor != next {
				t.Error("lost cursor")
			}
			next = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []map[string]string{{"thread_id": string(rune('a' + calls))}}, "next_cursor": next})
	}))
	defer server.Close()
	t.Setenv("CODEX_SWARM_RELAY_URL", server.URL)
	t.Setenv("CODEX_SWARM_HOST_ID", "linux")
	t.Setenv("CODEX_SWARM_RELAY_TOKEN", "1234567890123456")
	var out bytes.Buffer
	c := cli{out: &out, err: &out, now: time.Now}
	if err := c.run([]string{"relay", "tasks", "--host", "windows"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(out.String(), `"thread_id":"b"`) || !strings.Contains(out.String(), `"thread_id":"c"`) {
		t.Fatalf("missing paginated tasks: %s", out.String())
	}
}
