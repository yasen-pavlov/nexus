package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

// useFakeKeyring swaps the keyring backend for an in-memory map so tests never
// touch the real OS secret service (which would be non-deterministic and absent
// in CI). It returns the backing store for assertions.
func useFakeKeyring(t *testing.T) map[string]string {
	t.Helper()
	store := map[string]string{}
	oSet, oGet, oDel, oUsable := keyringSet, keyringGet, keyringDelete, keyringUsable
	t.Cleanup(func() {
		keyringSet, keyringGet, keyringDelete, keyringUsable = oSet, oGet, oDel, oUsable
	})
	keyringUsable = func() bool { return true }
	keyringSet = func(service, user, password string) error {
		store[service+"\x00"+user] = password
		return nil
	}
	keyringGet = func(service, user string) (string, error) {
		if v, ok := store[service+"\x00"+user]; ok {
			return v, nil
		}
		return "", keyring.ErrNotFound
	}
	keyringDelete = func(service, user string) error {
		key := service + "\x00" + user
		if _, ok := store[key]; !ok {
			return keyring.ErrNotFound // match real keyring.Delete on a missing entry
		}
		delete(store, key)
		return nil
	}
	return store
}

func TestStoreLoadRemoveToken(t *testing.T) {
	useFakeKeyring(t)
	const server = "http://nexus.local:8080"

	if _, ok := loadToken(server); ok {
		t.Fatal("expected no token before store")
	}
	if !storeToken(server, "nexus_pat_x") {
		t.Fatal("store should succeed with a usable keychain")
	}
	if tok, ok := loadToken(server); !ok || tok != "nexus_pat_x" {
		t.Fatalf("load = %q/%v", tok, ok)
	}
	removeToken(server)
	if _, ok := loadToken(server); ok {
		t.Fatal("token should be gone after remove")
	}
}

func TestKeyringUnavailableFallsBack(t *testing.T) {
	useFakeKeyring(t)
	keyringUsable = func() bool { return false }

	if storeToken("s", "t") {
		t.Fatal("store must report failure when keychain is unusable")
	}
	if _, ok := loadToken("s"); ok {
		t.Fatal("load must miss when keychain is unusable")
	}
	removeToken("s") // must not panic
}

func TestKeyringSetFailureFallsBack(t *testing.T) {
	useFakeKeyring(t)
	keyringSet = func(string, string, string) error { return keyring.ErrSetDataTooBig }

	if storeToken("s", "t") {
		t.Fatal("store must report failure when keyring.Set errors")
	}
}

func TestPersistCredentialsKeychain(t *testing.T) {
	isolateConfig(t) // installs the fake keyring + temp config dir
	cfg := &Config{ServerURL: "http://k:8080", Username: "muty"}

	where, err := persistCredentials(cfg, "nexus_pat_kc")
	if err != nil {
		t.Fatal(err)
	}
	if where != "the system keychain" {
		t.Fatalf("where = %q", where)
	}
	if cfg.Token != "" {
		t.Fatal("token must be kept out of the file when the keychain holds it")
	}
	if tok, ok := loadToken("http://k:8080"); !ok || tok != "nexus_pat_kc" {
		t.Fatalf("keychain token = %q/%v", tok, ok)
	}
	// The persisted file must carry metadata but no secret.
	saved, _ := LoadConfig()
	if saved.Token != "" || saved.Username != "muty" {
		t.Fatalf("file config = %+v", saved)
	}
}

func TestPersistCredentialsFileFallback(t *testing.T) {
	isolateConfig(t)
	keyringUsable = func() bool { return false } // force the file path

	cfg := &Config{ServerURL: "http://f:8080", Username: "muty"}
	where, err := persistCredentials(cfg, "nexus_pat_file")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "nexus_pat_file" {
		t.Fatalf("token should land in the file: %+v", cfg)
	}
	if where == "the system keychain" {
		t.Fatalf("where = %q, want a file path", where)
	}
	// The token must actually be on disk and absent from the keychain.
	saved, _ := LoadConfig()
	if saved.Token != "nexus_pat_file" {
		t.Fatalf("token not persisted to disk: %+v", saved)
	}
	if _, ok := loadToken("http://f:8080"); ok {
		t.Fatal("token must not be in the keychain on the file-fallback path")
	}
}

func TestPerServerIsolation(t *testing.T) {
	useFakeKeyring(t)
	const a = "http://a.nexus:8080"
	const b = "http://b.nexus:8080"

	if !storeToken(a, "nexus_pat_a") || !storeToken(b, "nexus_pat_b") {
		t.Fatal("stores should succeed")
	}
	if tok, ok := loadToken(a); !ok || tok != "nexus_pat_a" {
		t.Fatalf("server a load = %q/%v", tok, ok)
	}
	if tok, ok := loadToken(b); !ok || tok != "nexus_pat_b" {
		t.Fatalf("server b load = %q/%v", tok, ok)
	}
	// Removing one must not touch the other.
	removeToken(a)
	if _, ok := loadToken(a); ok {
		t.Fatal("server a should be gone")
	}
	if tok, ok := loadToken(b); !ok || tok != "nexus_pat_b" {
		t.Fatalf("server b must survive a's removal: %q/%v", tok, ok)
	}
}

func TestKeyringUsableLinuxGating(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("session-bus gating is Linux-specific")
	}
	// Exercise the real keyringUsable closure; control the socket-probe path.
	origSock := sessionBusSocket
	t.Cleanup(func() { sessionBusSocket = origSock })

	// No bus address and no socket → unusable (graceful file fallback).
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	sessionBusSocket = func() string { return filepath.Join(t.TempDir(), "nope-bus") }
	if keyringUsable() {
		t.Fatal("want unusable with no bus address and no socket")
	}

	// XDG_RUNTIME_DIR alone must NOT make it usable (the regressed case).
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if keyringUsable() {
		t.Fatal("XDG_RUNTIME_DIR must not, by itself, mark the keychain usable")
	}

	// An explicit bus address → usable.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	if !keyringUsable() {
		t.Fatal("want usable with DBUS_SESSION_BUS_ADDRESS set")
	}

	// No env var, but the session-bus socket exists → usable.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	sock := filepath.Join(t.TempDir(), "bus")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sessionBusSocket = func() string { return sock }
	if !keyringUsable() {
		t.Fatal("want usable when the session-bus socket exists")
	}

	// Empty socket path (user lookup failed) + no env → unusable.
	sessionBusSocket = func() string { return "" }
	if keyringUsable() {
		t.Fatal("want unusable when socket path is empty and no bus address")
	}
}
