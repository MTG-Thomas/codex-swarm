package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureTokenCreatesStablePrivateSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "daemon.token")
	first, resolved, err := EnsureToken(path)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := EnsureToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != daemonTokenBytes*2 || resolved != path {
		t.Fatalf("first=%q second=%q resolved=%q", first, second, resolved)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%o", info.Mode().Perm())
	}
}

func TestLoadTokenRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real")
	if err := os.WriteFile(realPath, []byte(strings.Repeat("a", daemonTokenBytes*2)), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "token")
	if err := os.Symlink("real", linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadToken(linkPath); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadTokenRejectsPermissiveUnixMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses DACL validation")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", daemonTokenBytes*2)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("error=%v", err)
	}
}

func TestAuthenticateProtectsEveryRouteExceptHealth(t *testing.T) {
	token := strings.Repeat("a", daemonTokenBytes*2)
	handler, err := Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), token)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, authorization string
		want                      int
	}{
		{name: "health", path: "/healthz", want: http.StatusNoContent},
		{name: "missing", path: "/status", want: http.StatusUnauthorized},
		{name: "wrong", path: "/workers", authorization: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "authorized read", path: "/workers", authorization: "Bearer " + token, want: http.StatusNoContent},
		{name: "authorized mutation", path: "/v1/messages", authorization: "Bearer " + token, want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			req.Header.Set("Authorization", test.authorization)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestClientAuthenticatesFromTokenFile(t *testing.T) {
	token := strings.Repeat("b", daemonTokenBytes*2)
	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = w.Write([]byte(`{"daemon":"running"}`))
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL, TokenFile: tokenFile}).Status(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestClientRefusesTokenForNonLoopbackEndpoint(t *testing.T) {
	client := Client{BaseURL: "http://example.com", Token: strings.Repeat("c", daemonTokenBytes*2)}
	_, err := client.Status(t.Context())
	if err == nil || !strings.Contains(err.Error(), "loopback HTTP") {
		t.Fatalf("error=%v", err)
	}
}
