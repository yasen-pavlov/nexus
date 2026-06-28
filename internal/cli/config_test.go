package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateConfig points the user config dir at a temp dir, clears the env
// overrides, and installs an in-memory keyring so each test sees a clean slate
// and never touches the real OS secret service. Relies on os.UserConfigDir
// honoring $XDG_CONFIG_HOME on non-macOS unix; skips where it doesn't.
func isolateConfig(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("os.UserConfigDir is not XDG-driven on this platform")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(envToken, "")
	t.Setenv(envURL, "")
	useFakeKeyring(t)
	return dir
}

func TestConfigRoundTripAndPerms(t *testing.T) {
	dir := isolateConfig(t)

	// Missing file → zero config, no error.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if cfg.Token != "" || cfg.ServerURL != "" {
		t.Fatalf("expected zero config, got %+v", cfg)
	}

	want := &Config{ServerURL: "http://nexus.local:8080", Token: "nexus_pat_x", TokenID: "id-1", Username: "muty"}
	if err := SaveConfig(want); err != nil {
		t.Fatalf("save: %v", err)
	}

	path := filepath.Join(dir, "nexus", "credentials.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if *got != *want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}

	// Clearing removes the file; a second clear is a no-op.
	if err := ClearConfig(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present after clear: %v", err)
	}
	if err := ClearConfig(); err != nil {
		t.Fatalf("second clear should be no-op: %v", err)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := isolateConfig(t)
	path := filepath.Join(dir, "nexus", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected parse error for malformed config")
	}
}

func TestNormalizeServerURL(t *testing.T) {
	cases := map[string]string{
		"nexus.bitnet.me":         "https://nexus.bitnet.me",
		"nexus.bitnet.me/":        "https://nexus.bitnet.me",
		"https://nexus.bitnet.me": "https://nexus.bitnet.me",
		"http://nexus.bitnet.me/": "http://nexus.bitnet.me",
		"localhost:8080":          "http://localhost:8080",
		"127.0.0.1:8080":          "http://127.0.0.1:8080",
		"example.com:9000":        "https://example.com:9000",
		"  nexus.bitnet.me  ":     "https://nexus.bitnet.me",
		"":                        "",
	}
	for in, want := range cases {
		if got := normalizeServerURL(in); got != want {
			t.Fatalf("normalizeServerURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveServerURL(t *testing.T) {
	t.Setenv(envURL, "")
	cfg := &Config{ServerURL: "http://stored:8080/"}

	if got := resolveServerURL("http://flag:9000/", cfg); got != "http://flag:9000" {
		t.Fatalf("flag precedence: got %q", got)
	}

	t.Setenv(envURL, "http://env:7000/")
	if got := resolveServerURL("", cfg); got != "http://env:7000" {
		t.Fatalf("env precedence: got %q", got)
	}

	t.Setenv(envURL, "")
	if got := resolveServerURL("", cfg); got != "http://stored:8080" {
		t.Fatalf("stored precedence: got %q", got)
	}
	if got := resolveServerURL("", &Config{}); got != defaultServerURL {
		t.Fatalf("default: got %q", got)
	}
}

func TestResolveToken(t *testing.T) {
	useFakeKeyring(t)
	const server = "http://s:8080"
	cfg := &Config{ServerURL: server, Token: "stored-tok"}

	// Three-way disagreement: env beats keychain beats file.
	storeToken(server, "kc-tok")
	t.Setenv(envToken, "env-tok")
	if got := resolveToken(cfg, server); got != "env-tok" {
		t.Fatalf("env must beat keychain+file: got %q", got)
	}

	t.Setenv(envToken, "")
	if got := resolveToken(cfg, server); got != "kc-tok" {
		t.Fatalf("keychain must beat file: got %q", got)
	}

	// File is the fallback once the keychain is empty.
	removeToken(server)
	if got := resolveToken(cfg, server); got != "stored-tok" {
		t.Fatalf("file fallback: got %q", got)
	}
	if got := resolveToken(nil, server); got != "" {
		t.Fatalf("nil config + empty keychain: got %q", got)
	}
}

func TestResolveTokenFileIsServerScoped(t *testing.T) {
	useFakeKeyring(t)
	t.Setenv(envToken, "")
	// A token saved for one server must not be returned for a different one.
	cfg := &Config{ServerURL: "http://a:8080", Token: "tok-a"}

	if got := resolveToken(cfg, "http://a:8080/"); got != "tok-a" {
		t.Fatalf("same server (trailing slash) should match: got %q", got)
	}
	if got := resolveToken(cfg, "http://b:8080"); got != "" {
		t.Fatalf("different server must not get a's file token: got %q", got)
	}
}
