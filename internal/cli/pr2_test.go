package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/model"
)

// fakePR2 is a stand-in Nexus covering the chats/ask/connectors surface.
type fakePR2 struct {
	*httptest.Server
	created int
	deleted int
}

func newFakePR2(t *testing.T) *fakePR2 {
	t.Helper()
	f := &fakePR2{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chats", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			f.created++
			writeOK(w, 201, map[string]any{"id": "00000000-0000-0000-0000-0000000000aa", "title": ""})
		case http.MethodGet:
			writeOK(w, 200, map[string]any{"chats": []map[string]any{{
				"id": "00000000-0000-0000-0000-0000000000aa", "title": "Recent chat",
				"updated_at": "2026-06-01T10:00:00Z", "default_model": "anthropic:claude-sonnet-4-6",
			}}, "total": 1})
		}
	})
	mux.HandleFunc("/api/chats/", f.handleChatByID)
	mux.HandleFunc("/api/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, 200, []map[string]any{{
			"id": "00000000-0000-0000-0000-0000000000bb", "type": "imap", "name": "Mail",
			"enabled": true, "shared": false, "schedule": "0 * * * *", "status": "active",
			"last_run": "2026-06-01T09:00:00Z",
		}})
	})
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, statusFor(r), []cliclient.SyncJob{{ID: "j1", ConnectorName: "Mail", ConnectorType: "imap", Status: "running"}})
	})
	mux.HandleFunc("/api/sync/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/sync/") {
		case "busy":
			writeErr(w, 409, "sync already running for Mail")
		case "missing":
			writeErr(w, 404, "connector not found")
		default:
			writeOK(w, 202, cliclient.SyncJob{ID: "j2", ConnectorName: "Mail", Status: "running"})
		}
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func statusFor(r *http.Request) int {
	if r.Method == http.MethodPost {
		return 202
	}
	return 200
}

func (f *fakePR2) handleChatByID(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/messages") {
		f.streamAnswer(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeOK(w, 200, map[string]any{
			"chat": map[string]any{"id": "00000000-0000-0000-0000-0000000000aa", "title": "A chat"},
			"messages": []model.ChatMessage{
				{Role: model.ChatRoleUser, Content: "hi"},
				{Role: model.ChatRoleAssistant, Content: "hello", Evidence: []model.ChunkPreview{{DocID: "d1"}}},
			},
		})
	case http.MethodDelete:
		f.deleted++
		w.WriteHeader(204)
	}
}

func (f *fakePR2) streamAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	if strings.Contains(body.Content, "boom") {
		_, _ = io.WriteString(w, "event: error\ndata: {\"message\":\"model exploded\"}\n\n")
		_, _ = io.WriteString(w, "event: done\ndata: {\"stop_reason\":\"error\"}\n\n")
		return
	}
	_, _ = io.WriteString(w, "event: evidence\ndata: {\"chunks\":[{\"id\":\"d1\",\"title\":\"Doc One\",\"source\":\"paperless\",\"source_name\":\"Paperless\",\"date\":\"2021-09-22\"}]}\n\n")
	// tool_result folds a new doc (d2) into the evidence union and re-mentions d1.
	_, _ = io.WriteString(w, "event: tool_result\ndata: {\"name\":\"nexus_search\",\"summary\":\"2 hits\",\"chunks\":[{\"id\":\"d1\"},{\"id\":\"d2\",\"title\":\"Doc Two\",\"source\":\"imap\"}]}\n\n")
	_, _ = io.WriteString(w, "event: text\ndata: {\"delta\":\"The answer is 42.\"}\n\n")
	_, _ = io.WriteString(w, "event: citation\ndata: {\"doc_id\":\"d1\",\"cited_text\":\"42\",\"span\":[0,3]}\n\n")
	_, _ = io.WriteString(w, "event: usage\ndata: {\"input\":10,\"output\":5}\n\n")
	_, _ = io.WriteString(w, "event: done\ndata: {\"stop_reason\":\"end_turn\",\"message_id\":\"m1\",\"duration_ms\":123}\n\n")
}

// seedAuth stores a token for server so authedClient resolves it.
func seedAuth(t *testing.T, server string) {
	t.Helper()
	if err := SaveConfig(&Config{ServerURL: server, Token: "nexus_pat_x"}); err != nil {
		t.Fatal(err)
	}
}

func TestAskCommand(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "", "ask", "what", "is", "the", "answer")
	if err != nil {
		t.Fatalf("ask: %v\n%s", err, out)
	}
	if !strings.Contains(out, "The answer is 42.") {
		t.Fatalf("missing streamed answer: %s", out)
	}
	// Default footer shows only the cited doc (d1), not the uncited d2.
	if !strings.Contains(out, "Sources:") || !strings.Contains(out, "Doc One") || strings.Contains(out, "Doc Two") {
		t.Fatalf("cited-only footer wrong: %s", out)
	}
	if !strings.Contains(out, "tokens in=10 out=5") {
		t.Fatalf("missing usage meta line: %s", out)
	}
	if f.created != 1 || f.deleted != 1 {
		t.Fatalf("ephemeral lifecycle wrong: created=%d deleted=%d", f.created, f.deleted)
	}
}

func TestAskShowAllSources(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	// --sources shows the full evidence union, including the tool_result-folded d2.
	out, err := run(t, "", "ask", "question", "--sources")
	if err != nil {
		t.Fatalf("ask --sources: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Doc One") || !strings.Contains(out, "Doc Two") {
		t.Fatalf("--sources should show the full union (d1+d2): %s", out)
	}
}

func TestAskJSON(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "", "ask", "question", "--json")
	if err != nil {
		t.Fatalf("ask --json: %v\n%s", err, out)
	}
	var payload struct {
		Answer  string `json:"answer"`
		Sources []struct {
			ID    string `json:"id"`
			Cited bool   `json:"cited"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	// The union carries both docs (d1 cited via citation, d2 folded from tool_result).
	if payload.Answer != "The answer is 42." || len(payload.Sources) != 2 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	cited := map[string]bool{}
	for _, s := range payload.Sources {
		cited[s.ID] = s.Cited
	}
	if !cited["d1"] || cited["d2"] {
		t.Fatalf("cited flags wrong: %+v", payload.Sources)
	}
}

func TestAskErrorFrameExitsNonZero(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	_, err := run(t, "", "ask", "boom")
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("expected error-frame failure, got %v", err)
	}
	if f.deleted != 1 {
		t.Fatal("temporary chat must be deleted even after an error frame")
	}
}

func TestChatsListGetDelete(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "", "chats", "list")
	if err != nil || !strings.Contains(out, "Recent chat") || !strings.Contains(out, "1 of 1 chats") {
		t.Fatalf("chats list: %v\n%s", err, out)
	}
	out, err = run(t, "", "chats", "get", "00000000-0000-0000-0000-0000000000aa")
	if err != nil || !strings.Contains(out, "A chat") || !strings.Contains(out, "hello") {
		t.Fatalf("chats get: %v\n%s", err, out)
	}
	out, err = run(t, "", "chats", "delete", "00000000-0000-0000-0000-0000000000aa")
	if err != nil || !strings.Contains(out, "Deleted chat") {
		t.Fatalf("chats delete: %v\n%s", err, out)
	}
}

func TestConnectorsListSyncStatus(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "", "connectors", "list")
	if err != nil || !strings.Contains(out, "Mail") || !strings.Contains(out, "imap") {
		t.Fatalf("connectors list: %v\n%s", err, out)
	}
	out, err = run(t, "", "connectors", "sync", "ok")
	if err != nil || !strings.Contains(out, "Started sync for Mail") {
		t.Fatalf("connectors sync: %v\n%s", err, out)
	}
	// A 409 (already running) is informational, not an error.
	out, err = run(t, "", "connectors", "sync", "busy")
	if err != nil || !strings.Contains(out, "already running") {
		t.Fatalf("connectors sync busy: %v\n%s", err, out)
	}
	// A 404 (not found / disabled / not yours) is a real error.
	if _, err := run(t, "", "connectors", "sync", "missing"); err == nil {
		t.Fatal("connectors sync of a missing connector should error")
	}
	out, err = run(t, "", "connectors", "sync", "--all")
	if err != nil || !strings.Contains(out, "Started sync for Mail (job j1)") {
		t.Fatalf("connectors sync --all: %v\n%s", err, out)
	}
	out, err = run(t, "", "connectors", "status")
	if err != nil || !strings.Contains(out, "Mail") || !strings.Contains(out, "running") {
		t.Fatalf("connectors status: %v\n%s", err, out)
	}
}
