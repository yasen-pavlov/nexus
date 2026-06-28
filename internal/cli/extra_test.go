package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigErrorsWithoutConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserConfigDir uses %AppData% on Windows, not HOME/XDG")
	}
	// With neither $XDG_CONFIG_HOME nor $HOME set, os.UserConfigDir errors, and
	// every config helper must surface that rather than panic.
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig should error without a config dir")
	}
	if err := SaveConfig(&Config{}); err == nil {
		t.Fatal("SaveConfig should error without a config dir")
	}
	if err := ClearConfig(); err == nil {
		t.Fatal("ClearConfig should error without a config dir")
	}
}

func TestLoginValidateTokenFailure(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "authentication required"})
	}))
	defer srv.Close()

	// A pasted token that the server rejects must fail validation, not persist
	// to either the file or the keychain.
	if _, err := run(t, "", "login", "--server", srv.URL, "--token", "nexus_pat_bad"); err == nil {
		t.Fatal("expected validation failure for a rejected token")
	}
	cfg, _ := LoadConfig()
	if cfg.Token != "" {
		t.Fatalf("rejected token should not be saved: %+v", cfg)
	}
	if _, ok := loadToken(srv.URL); ok {
		t.Fatal("rejected token should not reach the keychain")
	}
}

func TestPromptCredentialsValidation(t *testing.T) {
	if _, err := promptCredentials(strings.NewReader("\n\n"), io.Discard, ""); err == nil {
		t.Fatal("expected error for empty username")
	}
	if _, err := promptCredentials(strings.NewReader("muty\n\n"), io.Discard, ""); err == nil {
		t.Fatal("expected error for empty password")
	}

	// Preset username skips the username prompt and only reads the password.
	creds, err := promptCredentials(strings.NewReader("hunter2\n"), io.Discard, "preset")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.username != "preset" || creds.password != "hunter2" {
		t.Fatalf("unexpected creds: %+v", creds)
	}
}

func TestTokenNameNonEmpty(t *testing.T) {
	if tokenName() == "" {
		t.Fatal("token name must never be empty")
	}
}

func TestReadPasswordSeam(t *testing.T) {
	f, err := os.Open(os.DevNull) // a real *os.File that is not a terminal
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// Non-terminal *os.File → falls through to the buffered line read.
	got, err := readPassword(f, bufio.NewReader(strings.NewReader("frompipe\n")), io.Discard)
	if err != nil || got != "frompipe" {
		t.Fatalf("line path: %v %q", err, got)
	}

	// Stub the terminal read to exercise the handled=true success + error paths.
	orig := terminalPassword
	defer func() { terminalPassword = orig }()

	terminalPassword = func(*os.File, io.Writer) (string, bool, error) { return "masked", true, nil }
	if got, err := readPassword(f, bufio.NewReader(strings.NewReader("")), io.Discard); err != nil || got != "masked" {
		t.Fatalf("terminal success: %v %q", err, got)
	}

	terminalPassword = func(*os.File, io.Writer) (string, bool, error) { return "", true, errors.New("tty fail") }
	if _, err := readPassword(f, bufio.NewReader(strings.NewReader("")), io.Discard); err == nil {
		t.Fatal("terminal error path: want error")
	}
}

func TestSaveConfigMkdirError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path semantics differ on Windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(envToken, "")
	t.Setenv(envURL, "")
	// A regular file where the "nexus" config dir should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(dir, "nexus"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(&Config{Token: "x"}); err == nil {
		t.Fatal("expected MkdirAll error when the config dir path is a file")
	}
}

func TestMintTokenCreateFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			writeOK(w, 200, map[string]any{"token": "jwt"})
			return
		}
		writeErr(w, 500, "nope") // /api/tokens
	}))
	defer srv.Close()

	if _, _, err := mintToken(context.Background(), srv.URL, "u", "p", "name"); err == nil {
		t.Fatal("expected a CreateToken failure to propagate")
	}
}
