package remoteworkspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type fakeRunner struct {
	binary string
	args   []string
	stdin  []byte
	out    []byte
	err    error
	calls  int
}

func (f *fakeRunner) Run(_ context.Context, binary string, args []string, stdin []byte) ([]byte, error) {
	f.calls++
	f.binary = binary
	f.args = append([]string(nil), args...)
	f.stdin = append([]byte(nil), stdin...)
	return f.out, f.err
}

func TestSSHPrepareUsesFixedRemoteCommandAndEncodedRequest(t *testing.T) {
	fake := &fakeRunner{out: []byte(`{"path":"/home/thomas/.local/share/codex-swarm/workspaces/w-123","branch":"cs/w-123","repo_url":"git@github.com:MTG/repo.git","base_ref":"main"}`)}
	spec := Spec{
		WorkerID: "w-123",
		RepoURL:  "git@github.com:MTG/repo.git",
		Branch:   "cs/w-123",
		BaseRef:  "main",
		GitName:  "Codex Swarm",
		GitEmail: "codex@example.com",
	}
	result, err := (SSH{Binary: "ssh-test", Target: "thomas@remote", Jump: "root@jump", Runner: fake}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path == "" {
		t.Fatal("empty result path")
	}
	wantArgs := []string{"-o", "BatchMode=yes", "-J", "root@jump", "thomas@remote", "--", "python3", "-"}
	if fake.binary != "ssh-test" || !reflect.DeepEqual(fake.args, wantArgs) {
		t.Fatalf("command = %s %#v, want ssh-test %#v", fake.binary, fake.args, wantArgs)
	}
	match := regexp.MustCompile(`spec = json.loads\(base64.b64decode\("([A-Za-z0-9+/=]+)"\)\)`).FindSubmatch(fake.stdin)
	if len(match) != 2 {
		t.Fatalf("encoded request not found in script")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	var got Spec
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("request = %+v, want %+v", got, spec)
	}
}

func TestSSHPrepareRejectsUnsafeEndpoints(t *testing.T) {
	for _, provider := range []SSH{{Target: "host;id"}, {Target: "host", Jump: "jump$(id)"}} {
		_, err := provider.Prepare(context.Background(), Spec{WorkerID: "w-1", RepoURL: "git@example.com:owner/repo.git", Branch: "cs/w-1"})
		if err == nil {
			t.Fatalf("Prepare(%+v) error = nil", provider)
		}
	}
}

func TestValidateRepositoryURL(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/owner/repo.git",
		"https://git.example.test:8443/team/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"ssh://git.example.test:2222/team/repo.git",
		"git@github.com:owner/repo.git",
		"git.example.test:team/repo.git",
		"git@[2001:db8::1]:team/repo.git",
	} {
		t.Run("accept_"+raw, func(t *testing.T) {
			got, err := ValidateRepositoryURL(raw)
			if err != nil || got != raw {
				t.Fatalf("ValidateRepositoryURL() = %q, %v", got, err)
			}
		})
	}

	for _, raw := range []string{
		"https://token@github.com/owner/repo.git",
		"https://user:secret@github.com/owner/repo.git",
		"https://github.com/owner/repo.git?access_token=secret-query",
		"https://github.com/owner/repo.git#secret-fragment",
		"ssh://git:secret@github.com/owner/repo.git",
		"ssh://token%40example@github.com/owner/repo.git",
		"git@github.com:owner/repo.git?secret-query",
		"git@github.com:owner/repo.git#secret-fragment",
		"http://github.com/owner/repo.git",
		"file:///tmp/repo.git",
		"//github.com/owner/repo.git",
		"https:github.com/owner/repo.git",
		"https://github.com",
		"https://github.com/%zz/secret-malformed",
		"https://github.com/owner\\repo.git",
		" https://github.com/owner/repo.git",
		"https://github.com/owner/repo.git\nsecret",
	} {
		t.Run("reject_"+raw, func(t *testing.T) {
			got, err := ValidateRepositoryURL(raw)
			if err == nil || got != "" {
				t.Fatalf("ValidateRepositoryURL() = %q, %v", got, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
				t.Fatalf("error disclosed rejected URL: %v", err)
			}
		})
	}
}

func TestSSHPrepareRejectsRepositoryCredentialsBeforeRunningSSH(t *testing.T) {
	fake := &fakeRunner{}
	_, err := (SSH{Target: "host", Runner: fake}).Prepare(context.Background(), Spec{
		WorkerID: "w-1", RepoURL: "https://github.com/owner/repo.git?access_token=secret-query", Branch: "cs/w-1",
	})
	if err == nil || fake.calls != 0 {
		t.Fatalf("error = %v, runner calls = %d", err, fake.calls)
	}
	if strings.Contains(err.Error(), "secret-query") {
		t.Fatalf("error disclosed rejected URL: %v", err)
	}
}

func TestSSHPrepareUsesValidatedRepositoryURLInsteadOfRemoteReadback(t *testing.T) {
	fake := &fakeRunner{out: []byte(`{"path":"/home/user/workspaces/w-1","branch":"cs/w-1","repo_url":"https://user:secret@attacker.invalid/repo.git","base_ref":"main"}`)}
	result, err := (SSH{Target: "host", Runner: fake}).Prepare(context.Background(), Spec{
		WorkerID: "w-1", RepoURL: "git@github.com:owner/repo.git", Branch: "cs/w-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RepoURL != "git@github.com:owner/repo.git" {
		t.Fatalf("RepoURL = %q", result.RepoURL)
	}
}

func TestSSHPrepareErrorDoesNotRepeatRepositoryURL(t *testing.T) {
	fake := &fakeRunner{err: errors.New("connection failed")}
	_, err := (SSH{Target: "host", Runner: fake}).Prepare(context.Background(), Spec{
		WorkerID: "w-1", RepoURL: "git@github.com:owner/repo.git", Branch: "cs/w-1",
	})
	if err == nil || strings.Contains(err.Error(), "git@github.com") {
		t.Fatalf("error = %v", err)
	}
}
