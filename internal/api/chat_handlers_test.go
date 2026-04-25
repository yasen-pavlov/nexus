//go:build integration

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rag"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// fakeLLMRegistry is the test bridge that lets us drive the SSE
// endpoint without hitting a real provider.
type fakeLLMRegistry struct {
	gen  llm.Generator
	info llm.ModelInfo
}

func (f *fakeLLMRegistry) Get(_ string) (llm.Generator, llm.ModelInfo, error) {
	return f.gen, f.info, nil
}

func (f *fakeLLMRegistry) Models() []llm.ModelInfo { return []llm.ModelInfo{f.info} }

// scriptedGenerator emits a fixed sequence of llm.Events.
type scriptedGenerator struct{ events []llm.Event }

func (g *scriptedGenerator) Generate(_ context.Context, _ llm.GenerateRequest) (<-chan llm.Event, error) {
	out := make(chan llm.Event, len(g.events))
	for _, ev := range g.events {
		out <- ev
	}
	close(out)
	return out, nil
}

// chatTestEnv groups the deps + tokens for a chat handler test.
type chatTestEnv struct {
	st         *store.Store
	router     http.Handler
	ownerID    uuid.UUID
	ownerToken string
	otherID    uuid.UUID
	otherToken string
	adminID    uuid.UUID
	adminToken string
}

// newChatTestEnv wires the router with a stubbed registry + REAL
// SearchService (wrapped in the production RAGSearchProvider adapter).
// The test OpenSearch index is empty so retrieval returns 0 docs;
// every test then drives the LLM-side via the scripted generator.
func newChatTestEnv(t *testing.T, events []llm.Event) chatTestEnv {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	lm := NewLLMManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())

	gen := &scriptedGenerator{events: events}
	info := llm.ModelInfo{
		ID:                "anthropic:claude-sonnet-4-6",
		Provider:          "anthropic",
		BareID:            "claude-sonnet-4-6",
		SupportsCitations: true,
	}
	registry := &fakeLLMRegistry{gen: gen, info: info}

	searchService := NewSearchService(sc, em, rm, rankingMgr, zap.NewNop())
	orchestrator := rag.NewOrchestrator(rag.Deps{
		Registry: func() llm.Registry { return registry },
		Search:   NewRAGSearchProvider(searchService),
		Chats:    st,
		Cfg:      rag.DefaultConfig(),
		Log:      zap.NewNop(),
	})
	router := NewRouter(st, sc, p, cm, em, rm, lm, orchestrator, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())

	ownerID, ownerToken := createTestUser(t, st)
	otherID, otherToken := createTestUser(t, st)
	adminID, adminToken := createTestAdmin(t, st)

	return chatTestEnv{
		st:         st,
		router:     router,
		ownerID:    ownerID,
		ownerToken: ownerToken,
		otherID:    otherID,
		otherToken: otherToken,
		adminID:    adminID,
		adminToken: adminToken,
	}
}

// doChatRequest issues a JSON request and returns the recorder. Token
// can be "" for unauthenticated calls.
func doChatRequest(t *testing.T, router http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewBuffer(b)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeChatData(t *testing.T, body *bytes.Buffer, target any) {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, target); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

// --- CRUD tests ---

func TestCreateChat_HappyPath(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"title": "my chat", "default_model": "anthropic:claude-sonnet-4-6"},
	)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	if chat.UserID != env.ownerID {
		t.Errorf("user_id=%s want %s", chat.UserID, env.ownerID)
	}
	if chat.Title != "my chat" {
		t.Errorf("title=%q", chat.Title)
	}
}

func TestCreateChat_AnonymousRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", w.Code)
	}
}

func TestListChats_OnlyOwnVisible(t *testing.T) {
	env := newChatTestEnv(t, nil)

	// Owner creates two chats; other user creates one. List as owner should return 2.
	for _, title := range []string{"a", "b"} {
		w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
			map[string]string{"title": title})
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", title, w.Code)
		}
	}
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.otherToken,
		map[string]string{"title": "stranger"})
	if w.Code != http.StatusCreated {
		t.Fatalf("other create: %d", w.Code)
	}

	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats", env.ownerToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var resp listChatsResponse
	decodeChatData(t, w.Body, &resp)
	if resp.Total != 2 {
		t.Errorf("total=%d", resp.Total)
	}
	if len(resp.Chats) != 2 {
		t.Errorf("got %d chats", len(resp.Chats))
	}
	for _, c := range resp.Chats {
		if c.FirstMessagePreview != "" {
			t.Errorf("expected empty preview before any messages, got %q", c.FirstMessagePreview)
		}
	}
}

func TestGetChat_NonOwnerReturns404(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Other regular user → 404
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("other status=%d want 404", w.Code)
	}
	// Admin viewing other user's chat — 404 (admins NOT exempt).
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.adminToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("admin status=%d want 404", w.Code)
	}
	// Owner → 200.
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.ownerToken, nil)
	if w.Code != http.StatusOK {
		t.Errorf("owner status=%d", w.Code)
	}
}

func TestGetChat_AnonRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anon status=%d", w.Code)
	}
}

func TestGetChat_InvalidID(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodGet, "/api/chats/not-a-uuid", env.ownerToken, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestUpdateChat_OwnerCanRename(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	newTitle := "renamed"
	w = doChatRequest(t, env.router, http.MethodPatch, "/api/chats/"+chat.ID.String(), env.ownerToken,
		map[string]any{"title": newTitle})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var updated model.Chat
	decodeChatData(t, w.Body, &updated)
	if updated.Title != newTitle {
		t.Errorf("title=%q", updated.Title)
	}
}

func TestUpdateChat_NonOwnerReturns404(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	w = doChatRequest(t, env.router, http.MethodPatch, "/api/chats/"+chat.ID.String(), env.otherToken,
		map[string]string{"title": "bad"})
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d", w.Code)
	}
}

func TestDeleteChat_OwnerSucceeds(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	w = doChatRequest(t, env.router, http.MethodDelete, "/api/chats/"+chat.ID.String(), env.ownerToken, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", w.Code)
	}
	// And it's really gone
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.ownerToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("after-delete status=%d", w.Code)
	}
}

func TestDeleteChat_NonOwnerReturns404(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	w = doChatRequest(t, env.router, http.MethodDelete, "/api/chats/"+chat.ID.String(), env.otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d", w.Code)
	}
}

// --- SSE message endpoint ---

func TestPostMessage_StreamsRetrievingEvidenceTextDone(t *testing.T) {
	events := []llm.Event{
		{Kind: llm.EventText, TextDelta: "hi"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 5, OutputTokens: 2}},
	}
	env := newChatTestEnv(t, events)

	// Create chat
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Stream message
	body, _ := json.Marshal(map[string]string{"content": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rw.Code, rw.Body.String())
	}
	if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type=%q", ct)
	}

	// Parse the SSE frames
	frames := parseSSE(t, rw.Body.String())
	wantOrder := []string{"retrieving", "evidence", "text", "usage", "done"}
	if !containsInOrder(frames, wantOrder) {
		t.Errorf("frames missing required order %v: %+v", wantOrder, frames)
	}

	// Verify the assistant message was persisted.
	msgs, err := env.st.ListMessages(context.Background(), chat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages: %+v", len(msgs), msgs)
	}
	asst := msgs[1]
	if asst.Role != model.ChatRoleAssistant || asst.Content != "hi" || asst.StopReason != "end_turn" {
		t.Errorf("assistant=%+v", asst)
	}
	if asst.Usage == nil || asst.Usage.Input != 5 {
		t.Errorf("usage=%+v", asst.Usage)
	}
}

func TestPostMessage_AnonRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", rw.Code)
	}
}

func TestPostMessage_NonOwnerReturns404(t *testing.T) {
	events := []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}
	env := newChatTestEnv(t, events)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.otherToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status=%d", rw.Code)
	}
}

func TestPostMessage_AdminOnOtherUserChatReturns404(t *testing.T) {
	events := []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}
	env := newChatTestEnv(t, events)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("admin status=%d want 404 (admins not exempt from chat ownership)", rw.Code)
	}
}

func TestPostMessage_RequiresContent(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status=%d", rw.Code)
	}
}

func TestPostMessage_TokenViaQueryString(t *testing.T) {
	events := []llm.Event{
		{Kind: llm.EventText, TextDelta: "ok"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}
	env := newChatTestEnv(t, events)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "hi"})
	// EventSource cannot set headers so the SSE endpoint accepts ?token=
	req := httptest.NewRequest(http.MethodPost,
		"/api/chats/"+chat.ID.String()+"/messages?token="+env.ownerToken, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rw.Code, rw.Body.String())
	}
}

// --- helpers (SSE parsing) ---

type sseFrame struct {
	event string
	data  string
}

func parseSSE(t *testing.T, raw string) []sseFrame {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var frames []sseFrame
	var cur sseFrame
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" {
				frames = append(frames, cur)
				cur = sseFrame{}
			}
		}
	}
	return frames
}

func containsInOrder(frames []sseFrame, want []string) bool {
	wi := 0
	for _, f := range frames {
		if wi < len(want) && f.event == want[wi] {
			wi++
		}
		if wi == len(want) {
			return true
		}
	}
	return wi == len(want)
}

// keep imports useful even if some tests are removed
var _ = time.Now
var _ = uuid.New

// --- error-path coverage ---

func TestCreateChat_RejectsInvalidJSON(t *testing.T) {
	env := newChatTestEnv(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/chats", bytes.NewBufferString("{not json"))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = 10
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestUpdateChat_RejectsInvalidJSON(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	req := httptest.NewRequest(http.MethodPatch, "/api/chats/"+chat.ID.String(), bytes.NewBufferString("{not json"))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	env.router.ServeHTTP(w2, req)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w2.Code)
	}
}

func TestUpdateChat_NotFoundOnDeletedChat(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Delete the chat directly
	if err := env.st.DeleteChat(context.Background(), chat.ID); err != nil {
		t.Fatal(err)
	}
	title := "x"
	w2 := doChatRequest(t, env.router, http.MethodPatch, "/api/chats/"+chat.ID.String(), env.ownerToken,
		map[string]any{"title": title})
	if w2.Code != http.StatusNotFound {
		t.Errorf("status=%d", w2.Code)
	}
}

func TestDeleteChat_AnonRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	w = doChatRequest(t, env.router, http.MethodDelete, "/api/chats/"+chat.ID.String(), "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", w.Code)
	}
}

func TestPostMessage_RejectsInvalidJSON(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBufferString("{not json"))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	env.router.ServeHTTP(w2, req)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w2.Code)
	}
}

func TestListChats_DefaultsAndCaps(t *testing.T) {
	env := newChatTestEnv(t, nil)
	// Create one chat so we have something to list.
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code)
	}

	// limit=0 should fall back to default.
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats?limit=0&offset=-1", env.ownerToken, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
	// limit > maxChatListLimit clamps.
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats?limit=99999", env.ownerToken, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
}

func TestListChats_AnonRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodGet, "/api/chats", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", w.Code)
	}
}

func TestPostMessage_StreamsCitationAndErrorFrames(t *testing.T) {
	// Drive the orchestrator with events that exercise both the
	// citation frame branch and the error frame branch in writeRagEvent.
	events := []llm.Event{
		{Kind: llm.EventCitation, Citation: &llm.Citation{DocID: "doc-x", CitedText: "snippet", SpanStart: 0, SpanEnd: 5}},
		{Kind: llm.EventError, Err: errFromString("rate limit hit")},
	}
	env := newChatTestEnv(t, events)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status=%d", rw.Code)
	}
	frames := parseSSE(t, rw.Body.String())
	var sawCite, sawErr, sawDone bool
	for _, f := range frames {
		switch f.event {
		case "citation":
			sawCite = true
		case "error":
			sawErr = true
		case "done":
			sawDone = true
		}
	}
	if !sawCite {
		t.Errorf("no citation frame: %+v", frames)
	}
	if !sawErr {
		t.Errorf("no error frame: %+v", frames)
	}
	if !sawDone {
		t.Errorf("no done frame: %+v", frames)
	}
}

// errFromString returns an error with a custom message — used so
// scriptedGenerator can ship a deterministic Err payload.
func errFromString(s string) error { return &simpleErr{msg: s} }

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

// brokenRegistry always returns a "model not configured" error.
type brokenRegistry struct{}

func (b *brokenRegistry) Get(_ string) (llm.Generator, llm.ModelInfo, error) {
	return nil, llm.ModelInfo{}, errFromString("provider not configured")
}
func (b *brokenRegistry) Models() []llm.ModelInfo { return nil }

func TestPostMessage_RegistryError_Returns400(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	lm := NewLLMManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())

	searchService := NewSearchService(sc, em, rm, rankingMgr, zap.NewNop())
	orchestrator := rag.NewOrchestrator(rag.Deps{
		Registry: func() llm.Registry { return &brokenRegistry{} },
		Search:   NewRAGSearchProvider(searchService),
		Chats:    st,
		Cfg:      rag.DefaultConfig(),
		Log:      zap.NewNop(),
	})
	router := NewRouter(st, sc, p, cm, em, rm, lm, orchestrator, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())
	_, token := createTestUser(t, st)

	// Create chat with a default model so the orchestrator doesn't fall
	// back to registry.Models() (which the broken registry also rejects).
	w := doChatRequest(t, router, http.MethodPost, "/api/chats", token,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400 (registry error)", rw.Code)
	}
}

// TestChatHandlers_DBErrorsReturn500 wires a router around a closed
// store pool so every chat-handler DB call hits its 500-branch. We
// verify the response code; the handler logs and writeError do the
// rest.
func TestChatHandlers_DBErrorsReturn500(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	lm := NewLLMManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())

	// Seed a user FIRST while the store is still open, so we have a
	// valid JWT once we close the pool.
	userID, token := createTestUser(t, st)
	_ = userID

	// Now construct the router around the same store, but seed a
	// chat record that we'll try to read after the pool is closed.
	chat := &model.Chat{UserID: userID, Title: "x"}
	if err := st.CreateChat(context.Background(), chat); err != nil {
		t.Fatalf("seed chat: %v", err)
	}

	router := NewRouter(st, sc, p, cm, em, rm, lm, nil, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())

	// Close the pool — every subsequent request should 500.
	st.Close()

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/chats", map[string]string{"title": "x"}},
		{http.MethodGet, "/api/chats", nil},
		{http.MethodGet, "/api/chats/" + chat.ID.String(), nil},
		{http.MethodPatch, "/api/chats/" + chat.ID.String(), map[string]string{"title": "y"}},
		{http.MethodDelete, "/api/chats/" + chat.ID.String(), nil},
	}
	for _, c := range cases {
		t.Run(c.method+c.path, func(t *testing.T) {
			w := doChatRequest(t, router, c.method, c.path, token, c.body)
			if w.Code != http.StatusInternalServerError {
				t.Errorf("status=%d want 500 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestPostMessage_OrchestratorNilReturns503(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	lm := NewLLMManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())

	router := NewRouter(st, sc, p, cm, em, rm, lm, nil /* no orchestrator */, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())
	_, token := createTestUser(t, st)

	// Create chat
	w := doChatRequest(t, router, http.MethodPost, "/api/chats", token,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Post a message — orchestrator is nil → 503
	body, _ := json.Marshal(map[string]string{"content": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", rw.Code)
	}
}
