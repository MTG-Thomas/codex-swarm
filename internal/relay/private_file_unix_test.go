//go:build linux || darwin

package relay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFileUnixRejectsUnsafeFileAndPath(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openPrivateFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	permissive := filepath.Join(dir, "permissive.json")
	if err = os.WriteFile(permissive, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err = openPrivateFile(permissive); err == nil {
		f.Close()
		t.Fatal("accepted group/world-readable private file")
	}

	for _, mode := range []os.FileMode{0o770, 0o707} {
		writableParent := filepath.Join(dir, mode.String())
		if err = os.Mkdir(writableParent, 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(writableParent, mode); err != nil {
			t.Fatal(err)
		}
		unsafePath := filepath.Join(writableParent, "config.json")
		if err = os.WriteFile(unsafePath, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if f, err = openPrivateFile(unsafePath); err == nil {
			f.Close()
			t.Fatalf("accepted file beneath directory with mode %04o", mode)
		}
	}
}

func TestPrivateFileUnixRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(dir, "file-link.json")
	if err := os.Symlink(target, fileLink); err != nil {
		t.Fatal(err)
	}
	if f, err := openPrivateFile(fileLink); err == nil {
		f.Close()
		t.Fatal("accepted final symlink")
	}

	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(realDir, "inside.json")
	if err := os.WriteFile(inside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(dir, "dir-link")
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Fatal(err)
	}
	if f, err := openPrivateFile(filepath.Join(dirLink, "inside.json")); err == nil {
		f.Close()
		t.Fatal("accepted parent symlink")
	}
}

func TestPrivateConfigLoadersRejectPermissiveFiles(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "host.json")
	policy := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(host, []byte(`{"url":"https://relay.example.com","host":"test-host","token":"test-token-1234567890"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte(`{"enabled":true,"host":"test-host","os_user":"1000"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(host, filepath.Join(dir, "state.db")); err == nil {
		t.Fatal("host config loader accepted group/world-readable credentials")
	}
	if _, err := LoadEnrollmentPolicy(policy); err == nil {
		t.Fatal("enrollment policy loader accepted group/world-readable policy")
	}
}
