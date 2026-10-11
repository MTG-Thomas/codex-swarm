package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const daemonTokenBytes = 32

// TokenPath returns the default private bearer-token path for a daemon ledger.
func TokenPath(statePath string) string {
	return statePath + ".daemon-token"
}

// EnsureToken loads an existing private token or creates one exclusively.
func EnsureToken(path string) (string, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve daemon token path: %w", err)
	}
	if token, err := LoadToken(absPath); err == nil {
		return token, absPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return "", "", fmt.Errorf("create daemon token directory: %w", err)
	}
	raw := make([]byte, daemonTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate daemon token: %w", err)
	}
	token := hex.EncodeToString(raw)
	file, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		loaded, loadErr := LoadToken(absPath)
		return loaded, absPath, loadErr
	}
	if err != nil {
		return "", "", fmt.Errorf("create daemon token: %w", err)
	}
	if err = restrictCreatedToken(file); err != nil {
		file.Close()
		return "", "", fmt.Errorf("restrict daemon token: %w", err)
	}
	if _, err = io.WriteString(file, token+"\n"); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", fmt.Errorf("write daemon token: %w", err)
	}
	loaded, err := LoadToken(absPath)
	if err != nil {
		return "", "", fmt.Errorf("validate created daemon token: %w", err)
	}
	return loaded, absPath, nil
}

// LoadToken reads a stable regular token file and rejects permissive Unix mode.
func LoadToken(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve daemon token path: %w", err)
	}
	before, err := os.Lstat(absPath)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("daemon token must be a regular file: %s", absPath)
	}
	file, err := openDaemonToken(absPath)
	if err != nil {
		return "", fmt.Errorf("open private daemon token: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect daemon token: %w", err)
	}
	if !os.SameFile(before, opened) {
		return "", fmt.Errorf("daemon token changed while opening: %s", absPath)
	}
	data, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return "", fmt.Errorf("read daemon token: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() {
		return "", fmt.Errorf("daemon token changed while reading: %s", absPath)
	}
	token := strings.TrimSpace(string(data))
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != daemonTokenBytes {
		return "", errors.New("daemon token must contain exactly 32 bytes encoded as hexadecimal")
	}
	return token, nil
}

// Authenticate protects every daemon route except the liveness-only healthz.
func Authenticate(next http.Handler, token string) (http.Handler, error) {
	if _, err := hex.DecodeString(token); err != nil || len(token) != daemonTokenBytes*2 {
		return nil, errors.New("daemon authentication token is invalid")
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		provided := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeRouteError(w, r, http.StatusUnauthorized, "authentication_required", "daemon bearer authentication is required")
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}
