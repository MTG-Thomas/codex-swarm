//go:build windows

package relay

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateFileWindowsRejectsPermissiveACL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openPrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if output, err := exec.Command("icacls.exe", path, "/grant", "*S-1-1-0:(R)").CombinedOutput(); err != nil {
		t.Fatalf("grant test ACL: %v: %s", err, output)
	}
	if f, err = openPrivateFile(path); err == nil {
		f.Close()
		t.Fatal("accepted private file readable by Everyone")
	}
}

func TestPrivateFileWindowsRejectsReparsePoint(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Windows runner cannot create a test symlink: %v", err)
	}
	if f, err := openPrivateFile(link); err == nil {
		f.Close()
		t.Fatal("accepted reparse-point private file")
	}
}

func TestPrivateFileWindowsRejectsReplaceableParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "replaceable")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner := "*" + user.User.Sid.String() + ":(F)"
	if output, err := exec.Command("icacls.exe", path, "/inheritance:r", "/grant:r", owner, "*S-1-5-18:(F)", "*S-1-5-32-544:(F)").CombinedOutput(); err != nil {
		t.Fatalf("protect test file ACL: %v: %s", err, output)
	}
	if output, err := exec.Command("icacls.exe", dir, "/grant", "*S-1-1-0:(F)").CombinedOutput(); err != nil {
		t.Fatalf("make test parent replaceable: %v: %s", err, output)
	}
	if f, err := openPrivateFile(path); err == nil {
		f.Close()
		t.Fatal("accepted file beneath a replaceable directory")
	}
}
