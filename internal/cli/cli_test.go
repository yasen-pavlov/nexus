package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/model"
)

// fakeNexus is a minimal stand-in for the Nexus API, covering the endpoints the
// CLI touches. recordedAuth captures the last Authorization header seen.
type fakeNexus struct {
	*httptest.Server
	lastAuth string
}

func newFakeNexus(t *testing.T) *fakeNexus {
	t.Helper()
	f := &fakeNexus{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "muty" || body["password"] != "testtest" {
			writeErr(w, 400, "invalid username or password")
			return
		}
		writeOK(w, 200, map[string]any{"token": "jwt-abc"})
	})
	mux.HandleFunc("/api/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-abc" {
			writeErr(w, 403, "this action requires an interactive session")
			return
		}
		writeOK(w, 201, map[string]any{"token": "nexus_pat_minted", "meta": model.APIToken{Name: "nexus-cli"}})
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		if f.lastAuth == "" {
			writeErr(w, 401, "authentication required")
			return
		}
		writeOK(w, 200, map[string]any{"username": "muty", "role": "admin"})
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		writeOK(w, 200, model.SearchResult{
			Query:      r.URL.Query().Get("q"),
			TotalCount: 1,
			Documents: []model.DocumentHit{{
				Document: model.Document{Title: "Quarterly report", SourceType: "paperless", SourceName: "docs"},
				Headline: "the <mark>quarterly</mark> numbers",
			}},
		})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func writeOK(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// run executes the root command with args, capturing combined output.
func run(t *testing.T, in string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd("test", "abc", "today")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(in))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestLoginWithPastedToken(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)

	out, err := run(t, "", "login", "--server", f.URL, "--token", "nexus_pat_pasted")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Logged in as") || !strings.Contains(out, "system keychain") {
		t.Fatalf("output missing confirmation: %s", out)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// The secret lives in the keychain; the file carries only metadata.
	if cfg.Token != "" || cfg.ServerURL != f.URL {
		t.Fatalf("file config should hold metadata only: %+v", cfg)
	}
	if tok, ok := loadToken(f.URL); !ok || tok != "nexus_pat_pasted" {
		t.Fatalf("keychain token = %q/%v", tok, ok)
	}
	if f.lastAuth != "Bearer nexus_pat_pasted" {
		t.Fatalf("token not used for /me: %q", f.lastAuth)
	}
}

func TestLoginInteractiveMintsToken(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)

	out, err := run(t, "muty\ntesttest\n", "login", "--server", f.URL)
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if tok, ok := loadToken(f.URL); !ok || tok != "nexus_pat_minted" {
		t.Fatalf("expected minted token in keychain, got %q/%v", tok, ok)
	}
}

func TestLoginInteractiveBadPassword(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)

	if _, err := run(t, "muty\nwrongpass\n", "login", "--server", f.URL); err == nil {
		t.Fatal("expected login error with wrong password")
	}
}

func TestLogout(t *testing.T) {
	isolateConfig(t)
	const server = "http://x"
	if err := SaveConfig(&Config{ServerURL: server, TokenID: "id-1", Username: "muty"}); err != nil {
		t.Fatal(err)
	}
	storeToken(server, "nexus_pat_x") // secret lives in the keychain

	out, err := run(t, "", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(out, "cleared") || !strings.Contains(out, "server-side") {
		t.Fatalf("unexpected logout output: %s", out)
	}
	if _, ok := loadToken(server); ok {
		t.Fatal("keychain token not removed on logout")
	}
	cfg, _ := LoadConfig()
	if cfg.Token != "" || cfg.Username != "" {
		t.Fatalf("file not cleared: %+v", cfg)
	}

	// Logging out again reports nothing stored.
	out, err = run(t, "", "logout")
	if err != nil || !strings.Contains(out, "No stored credentials") {
		t.Fatalf("second logout: %v / %s", err, out)
	}
}

func TestSearchCommand(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)
	if err := SaveConfig(&Config{ServerURL: f.URL, Token: "nexus_pat_x"}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "search", "quarterly", "report")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Quarterly report") || !strings.Contains(out, "paperless") {
		t.Fatalf("missing rendered hit: %s", out)
	}
	// Highlight markup must be stripped from the human view.
	if strings.Contains(out, "<mark>") {
		t.Fatalf("highlight tags leaked into output: %s", out)
	}

	// --json passes through raw.
	jsonOut, err := run(t, "", "search", "quarterly", "--json", "--server", f.URL)
	if err != nil {
		t.Fatalf("search --json: %v", err)
	}
	if !strings.Contains(jsonOut, `"documents"`) {
		t.Fatalf("expected JSON output, got: %s", jsonOut)
	}
}

func TestSearchNotLoggedIn(t *testing.T) {
	isolateConfig(t)
	_, err := run(t, "", "search", "anything")
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("expected not-authenticated error, got %v", err)
	}
}

func TestSearchUsesKeychainToken(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)
	// Token only in the keychain (no file token) — search must read it and
	// forward it as the Bearer credential.
	if err := SaveConfig(&Config{ServerURL: f.URL, Username: "muty"}); err != nil {
		t.Fatal(err)
	}
	storeToken(f.URL, "nexus_pat_kc")

	out, err := run(t, "", "search", "invoice", "--server", f.URL)
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if f.lastAuth != "Bearer nexus_pat_kc" {
		t.Fatalf("search did not forward the keychain token: %q", f.lastAuth)
	}
}

func TestLoginTokenFromStdin(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)

	out, err := run(t, "nexus_pat_stdin\n", "login", "--server", f.URL, "--token", "-")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if tok, ok := loadToken(f.URL); !ok || tok != "nexus_pat_stdin" {
		t.Fatalf("stdin token not stored: %q/%v", tok, ok)
	}
}

func TestLoginWarnsWhenEnvTokenSet(t *testing.T) {
	isolateConfig(t)
	f := newFakeNexus(t)
	t.Setenv(envToken, "nexus_pat_envoverride")

	out, err := run(t, "", "login", "--server", f.URL, "--token", "nexus_pat_pasted")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "overrides stored credentials") {
		t.Fatalf("expected env-shadow warning, got: %s", out)
	}
}

func TestLogoutServerFlagClearsOrphan(t *testing.T) {
	isolateConfig(t)
	// An orphaned keychain entry for server A while the file points at B.
	storeToken("http://a:8080", "nexus_pat_a")
	if err := SaveConfig(&Config{ServerURL: "http://b:8080", Username: "muty"}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "logout", "--server", "http://a:8080")
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	if _, ok := loadToken("http://a:8080"); ok {
		t.Fatal("orphaned keychain entry for server A was not cleared")
	}
}

func TestWarnInsecureServer(t *testing.T) {
	cases := []struct {
		server string
		warn   bool
	}{
		{"http://nexus.lan:8080", true},
		{"http://192.168.1.5:8080", true},
		{"http://localhost:8080", false},
		{"http://127.0.0.1:8080", false},
		{"https://nexus.example.com", false},
	}
	for _, c := range cases {
		var b bytes.Buffer
		warnInsecureServer(&b, c.server)
		got := strings.Contains(b.String(), "plain HTTP")
		if got != c.warn {
			t.Fatalf("%s: warn=%v want %v (out=%q)", c.server, got, c.warn, b.String())
		}
	}
}
