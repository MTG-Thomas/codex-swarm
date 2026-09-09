package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRejectsUnsafeOrigins(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=x"} {
		c := Client{URL: origin, Host: "linux", Token: "1234567890123456"}
		if c.Validate() == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
}
func TestClientNeverFollowsRedirectsWithHostToken(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := Client{URL: server.URL, Host: "linux", Token: "1234567890123456"}
	if c.Call(context.Background(), "GET", "/v1/tasks", nil, nil) == nil || hit {
		t.Fatal("followed credential-bearing redirect")
	}
}
func TestClientBoundedResponsesAndAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Swarm-Host") != "linux" || r.Header.Get("Authorization") != "Bearer 1234567890123456" {
			t.Error("missing host authentication")
		}
		_, _ = w.Write(make([]byte, (1<<20)+1))
	}))
	defer server.Close()
	c := Client{URL: server.URL, Host: "linux", Token: "1234567890123456"}
	if c.Call(context.Background(), "GET", "/v1/tasks", nil, nil) == nil {
		t.Fatal("accepted oversized response")
	}
}
