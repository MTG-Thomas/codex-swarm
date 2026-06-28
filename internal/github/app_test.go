package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppIssueMetadataProviderFetchesIssue(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
			t.Fatalf("%s missing bearer auth", r.URL.Path)
		}
		seen[r.URL.Path] = r.Method
		switch r.URL.Path {
		case "/repos/MTG-Thomas/codex-swarm/installation":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 12345})
		case "/app/installations/12345/access_tokens":
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s, want POST", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "installation-token"})
		case "/repos/MTG-Thomas/codex-swarm/issues/46":
			if r.Header.Get("Authorization") != "Bearer installation-token" {
				t.Fatalf("issue auth = %q, want installation token", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"title": "Dispatch issue", "body": "Acceptance criteria"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewAppIssueMetadataProvider(AppConfig{
		AppID:         4163935,
		PrivateKeyPEM: keyPEM,
		APIURL:        server.URL,
		Now:           func() time.Time { return time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewAppIssueMetadataProvider() error = %v", err)
	}
	issue, err := provider.IssueMetadata(context.Background(), "MTG-Thomas/codex-swarm#46")
	if err != nil {
		t.Fatalf("IssueMetadata() error = %v", err)
	}
	if issue.Ref != "MTG-Thomas/codex-swarm#46" || issue.Title != "Dispatch issue" || issue.Body != "Acceptance criteria" {
		t.Fatalf("issue = %#v", issue)
	}
	for path, method := range map[string]string{
		"/repos/MTG-Thomas/codex-swarm/installation": "GET",
		"/app/installations/12345/access_tokens":     "POST",
		"/repos/MTG-Thomas/codex-swarm/issues/46":    "GET",
	} {
		if seen[path] != method {
			t.Fatalf("seen[%s] = %q, want %q", path, seen[path], method)
		}
	}
}

func TestAppIssueMetadataProviderUsesConfiguredInstallationID(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	seenInstallationLookup := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/MTG-Thomas/codex-swarm/installation":
			seenInstallationLookup = true
			http.Error(w, "unexpected lookup", http.StatusTeapot)
		case "/app/installations/777/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "installation-token"})
		case "/repos/MTG-Thomas/codex-swarm/issues/46":
			_ = json.NewEncoder(w).Encode(map[string]string{"title": "Issue", "body": "Body"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewAppIssueMetadataProvider(AppConfig{
		AppID:          4163935,
		InstallationID: 777,
		PrivateKeyPEM:  keyPEM,
		APIURL:         server.URL,
	})
	if err != nil {
		t.Fatalf("NewAppIssueMetadataProvider() error = %v", err)
	}
	if _, err := provider.IssueMetadata(context.Background(), "MTG-Thomas/codex-swarm#46"); err != nil {
		t.Fatalf("IssueMetadata() error = %v", err)
	}
	if seenInstallationLookup {
		t.Fatal("IssueMetadata() looked up repo installation despite configured installation id")
	}
}

func TestNewIssueMetadataProviderFromEnvUsesGitHubApp(t *testing.T) {
	keyFile := writeTestPrivateKey(t)
	t.Setenv("CODEX_SWARM_GITHUB_APP_ID", "4163935")
	t.Setenv("CODEX_SWARM_GITHUB_APP_CLIENT_ID", "Iv23liOKhd5ZCLFPBgbP")
	t.Setenv("CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE", keyFile)
	t.Setenv("CODEX_SWARM_GITHUB_APP_INSTALLATION_ID", "777")

	provider, err := NewIssueMetadataProviderFromEnv()
	if err != nil {
		t.Fatalf("NewIssueMetadataProviderFromEnv() error = %v", err)
	}
	if _, ok := provider.(*AppIssueMetadataProvider); !ok {
		t.Fatalf("provider = %T, want *AppIssueMetadataProvider", provider)
	}
}

func TestNewAppIssueMetadataProviderAcceptsClientIDIssuer(t *testing.T) {
	provider, err := NewAppIssueMetadataProvider(AppConfig{
		Issuer:        "Iv23liOKhd5ZCLFPBgbP",
		PrivateKeyPEM: testPrivateKeyPEM(t),
	})
	if err != nil {
		t.Fatalf("NewAppIssueMetadataProvider() error = %v", err)
	}
	if got := provider.issuer(); got != "Iv23liOKhd5ZCLFPBgbP" {
		t.Fatalf("issuer() = %q, want client id", got)
	}
}

func TestAppIssueMetadataProviderErrorsIncludeIssueRef(t *testing.T) {
	provider, err := NewAppIssueMetadataProvider(AppConfig{
		AppID:         4163935,
		PrivateKeyPEM: testPrivateKeyPEM(t),
		APIURL:        "https://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("NewAppIssueMetadataProvider() error = %v", err)
	}
	_, err = provider.IssueMetadata(context.Background(), "MTG-Thomas/codex-swarm#46")
	if err == nil {
		t.Fatal("IssueMetadata() error = nil, want connection error")
	}
	if !strings.Contains(err.Error(), "MTG-Thomas/codex-swarm#46") {
		t.Fatalf("IssueMetadata() error = %v, want issue ref context", err)
	}
}

func TestErrorIssueMetadataProviderIncludesIssue(t *testing.T) {
	cause := errors.New("bad env")
	_, err := (ErrorIssueMetadataProvider{Err: cause}).IssueMetadata(context.Background(), "MTG-Thomas/codex-swarm#46")
	if err == nil {
		t.Fatal("IssueMetadata() error = nil, want configured error")
	}
	if !strings.Contains(err.Error(), "MTG-Thomas/codex-swarm#46") || !errors.Is(err, cause) {
		t.Fatalf("IssueMetadata() error = %v, want issue context and wrapped cause", err)
	}
}

func TestNewIssueMetadataProviderFromEnvFallsBackToCLI(t *testing.T) {
	provider, err := NewIssueMetadataProviderFromEnv()
	if err != nil {
		t.Fatalf("NewIssueMetadataProviderFromEnv() error = %v", err)
	}
	if _, ok := provider.(CLIssueMetadataProvider); !ok {
		t.Fatalf("provider = %T, want CLIssueMetadataProvider", provider)
	}
}

func TestNewIssueMetadataProviderFromEnvFallsBackToCLIWhenAppKeyIsNotReadable(t *testing.T) {
	t.Setenv("CODEX_SWARM_GITHUB_APP_ID", "4163935")
	t.Setenv("CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE", "/system-only/app.pem")

	provider, err := newIssueMetadataProviderFromEnv(func(string) ([]byte, error) {
		return nil, os.ErrPermission
	})
	if err != nil {
		t.Fatalf("newIssueMetadataProviderFromEnv() error = %v", err)
	}
	if _, ok := provider.(CLIssueMetadataProvider); !ok {
		t.Fatalf("provider = %T, want CLIssueMetadataProvider", provider)
	}
}

func writeTestPrivateKey(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/app.pem"
	if err := os.WriteFile(path, testPrivateKeyPEM(t), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return path
}

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}
