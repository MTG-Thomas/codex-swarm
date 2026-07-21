package remoteworkspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

type fakeRunner struct {
	binary string
	args   []string
	stdin  []byte
	out    []byte
}

func (f *fakeRunner) Run(_ context.Context, binary string, args []string, stdin []byte) ([]byte, error) {
	f.binary = binary
	f.args = append([]string(nil), args...)
	f.stdin = append([]byte(nil), stdin...)
	return f.out, nil
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
		_, err := provider.Prepare(context.Background(), Spec{WorkerID: "w-1", RepoURL: "repo", Branch: "cs/w-1"})
		if err == nil {
			t.Fatalf("Prepare(%+v) error = nil", provider)
		}
	}
}

func TestSSHPrepareRejectsRepositoryCredentials(t *testing.T) {
	_, err := (SSH{Target: "host"}).Prepare(context.Background(), Spec{
		WorkerID: "w-1", RepoURL: "https://token@github.com/owner/repo.git", Branch: "cs/w-1",
	})
	if err == nil || !regexp.MustCompile(`must not contain credentials`).MatchString(err.Error()) {
		t.Fatalf("error = %v", err)
	}
}
