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
// endpoint without hitting a real provider. When the rewriter slot is
// populated, Get(rewriter.ID) returns the rewriter; everything else
// falls through to the main generator.
type fakeLLMRegistry struct {
	gen          llm.Generator
	info         llm.ModelInfo
	rewriterGen  llm.Generator
	rewriterInfo llm.ModelInfo
}

func (f *fakeLLMRegistry) Get(id string) (llm.Generator, llm.ModelInfo, error) {
	if f.rewriterGen != nil && id == f.rewriterInfo.ID {
		return f.rewriterGen, f.rewriterInfo, nil
	}
	return f.gen, f.info, nil
}

func (f *fakeLLMRegistry) Models() []llm.ModelInfo {
	out := []llm.ModelInfo{f.info}
	if f.rewriterGen != nil {
		out = append(out, f.rewriterInfo)
	}
	return out
}

func (f *fakeLLMRegistry) AllConfiguredModels() []llm.ModelInfo {
	return f.Models()
}

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

// multiCallScriptedGenerator emits a different scripted slice on each
// successive Generate call. Used for Phase 5 multi-round SSE drain
// tests where round 1 stops on tool_use and round 2 finishes the turn.
type multiCallScriptedGenerator struct {
	events [][]llm.Event
	calls  int
}

func (g *multiCallScriptedGenerator) Generate(_ context.Context, _ llm.GenerateRequest) (<-chan llm.Event, error) {
	idx := g.calls
	if idx >= len(g.events) {
		idx = len(g.events) - 1
	}
	g.calls++
	evs := g.events[idx]
	out := make(chan llm.Event, len(evs))
	for _, ev := range evs {
		out <- ev
	}
	close(out)
	return out, nil
}

// rewriterScript builds a scripted-generator that emits a single
// directive line + body so the orchestrator's rewriter parser captures
// it. Used by the SSE-drain tests below to exercise the rewriter +
// skip-retrieval flows end-to-end through the HTTP handler.
func rewriterScript(directive, body string) *scriptedGenerator {
	return &scriptedGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: directive + "\n" + body},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
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
	return newChatTestEnvWith(t, events, chatTestOpts{})
}

// chatTestOpts plumb optional rewriter + settings + custom main
// generator into the test env. When mainGen is non-nil, it replaces
// the scriptedGenerator built from the `events` slice — useful for
// multi-round / streaming-tool-call tests where one call's events
// aren't sufficient.
type chatTestOpts struct {
	rewriterGen  llm.Generator
	rewriterInfo llm.ModelInfo
	settings     rag.Settings
	mainGen      llm.Generator
	mainInfo     *llm.ModelInfo // nil → default Anthropic Sonnet info
}

func newChatTestEnvWith(t *testing.T, events []llm.Event, opts chatTestOpts) chatTestEnv {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	lm := NewLLMManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())

	var gen llm.Generator = &scriptedGenerator{events: events}
	if opts.mainGen != nil {
		gen = opts.mainGen
	}
	info := llm.ModelInfo{
		ID:                "anthropic:claude-sonnet-4-6",
		Provider:          "anthropic",
		BareID:            "claude-sonnet-4-6",
		SupportsCitations: true,
		SupportsTools:     true,
	}
	if opts.mainInfo != nil {
		info = *opts.mainInfo
	}
	registry := &fakeLLMRegistry{
		gen:          gen,
		info:         info,
		rewriterGen:  opts.rewriterGen,
		rewriterInfo: opts.rewriterInfo,
	}

	searchService := NewSearchService(sc, em, rm, rankingMgr, zap.NewNop())
	settings := opts.settings
	orchestrator := rag.NewOrchestrator(rag.Deps{
		Registry: func() llm.Registry { return registry },
		Settings: func() rag.Settings { return settings },
		Search:   NewRAGSearchProvider(searchService),
		Chats:    st,
		Cfg:      rag.DefaultConfig(),
		Log:      zap.NewNop(),
	})
	router := NewRouter(st, sc, p, cm, em, rm, lm, NewRAGManager(st, zap.NewNop()), orchestrator, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())

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

// --- message feedback endpoint ---

func feedbackPath(chatID, msgID string) string {
	return "/api/chats/" + chatID + "/messages/" + msgID + "/feedback"
}

func seedAssistantMessage(t *testing.T, env chatTestEnv, chatID uuid.UUID) uuid.UUID {
	t.Helper()
	msg := &model.ChatMessage{ChatID: chatID, Role: model.ChatRoleAssistant, Content: "answer"}
	if err := env.st.AppendMessage(context.Background(), msg); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return msg.ID
}

func TestSetMessageFeedback_OwnerRoundTripAndClear(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	msgID := seedAssistantMessage(t, env, chat.ID)
	path := feedbackPath(chat.ID.String(), msgID.String())

	// Rate up.
	w = doChatRequest(t, env.router, http.MethodPut, path, env.ownerToken, map[string]any{"feedback": "up"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("up status=%d body=%s", w.Code, w.Body.String())
	}
	// Persisted: GET chat shows feedback "up".
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.ownerToken, nil)
	var detail chatDetailResponse
	decodeChatData(t, w.Body, &detail)
	if len(detail.Messages) != 1 || detail.Messages[0].Feedback == nil || *detail.Messages[0].Feedback != "up" {
		t.Fatalf("feedback not persisted: %+v", detail.Messages)
	}

	// Clear (null).
	w = doChatRequest(t, env.router, http.MethodPut, path, env.ownerToken, map[string]any{"feedback": nil})
	if w.Code != http.StatusNoContent {
		t.Fatalf("clear status=%d", w.Code)
	}
	w = doChatRequest(t, env.router, http.MethodGet, "/api/chats/"+chat.ID.String(), env.ownerToken, nil)
	var afterClear chatDetailResponse
	decodeChatData(t, w.Body, &afterClear)
	if afterClear.Messages[0].Feedback != nil {
		t.Errorf("feedback should be cleared, got %v", *afterClear.Messages[0].Feedback)
	}
}

func TestSetMessageFeedback_InvalidValueRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	msgID := seedAssistantMessage(t, env, chat.ID)
	w = doChatRequest(t, env.router, http.MethodPut, feedbackPath(chat.ID.String(), msgID.String()),
		env.ownerToken, map[string]any{"feedback": "meh"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestSetMessageFeedback_NonOwnerReturns404(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	msgID := seedAssistantMessage(t, env, chat.ID)
	w = doChatRequest(t, env.router, http.MethodPut, feedbackPath(chat.ID.String(), msgID.String()),
		env.otherToken, map[string]any{"feedback": "up"})
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestSetMessageFeedback_AnonRejected(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	w = doChatRequest(t, env.router, http.MethodPut, feedbackPath(chat.ID.String(), uuid.New().String()),
		"", map[string]any{"feedback": "up"})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

func TestSetMessageFeedback_UnknownMessageReturns404(t *testing.T) {
	env := newChatTestEnv(t, nil)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	w = doChatRequest(t, env.router, http.MethodPut, feedbackPath(chat.ID.String(), uuid.New().String()),
		env.ownerToken, map[string]any{"feedback": "up"})
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
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

func TestPostMessage_RejectsTokenViaQueryString(t *testing.T) {
	// The chat-message stream is a POST issued with fetch(), which sets the
	// Authorization header — so it uses header-only auth. A URL-borne ?token=
	// must NOT be accepted here (that would leak the bearer credential into
	// logs/history); the query-param path is confined to the EventSource
	// sync-progress GETs.
	events := []llm.Event{
		{Kind: llm.EventText, TextDelta: "ok"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}
	env := newChatTestEnv(t, events)
	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken, nil)
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "hi"})

	// Query-string token only → rejected.
	req := httptest.NewRequest(http.MethodPost,
		"/api/chats/"+chat.ID.String()+"/messages?token="+env.ownerToken, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("query-token POST: status=%d, want 401 (%s)", rw.Code, rw.Body.String())
	}

	// Same request with the Authorization header → accepted.
	req = httptest.NewRequest(http.MethodPost,
		"/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw = httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("header POST: status=%d, want 200 (%s)", rw.Code, rw.Body.String())
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

// TestPostMessage_StreamsToolStartAndResultFrames drives a 2-round
// turn (round 1 stops on tool_use → dispatch nexus_search → round 2
// finishes). Asserts the SSE stream carries `tool_start` + `tool_result`
// frames in the expected order, and that both frame payloads parse.
func TestPostMessage_StreamsToolStartAndResultFrames(t *testing.T) {
	gen := &multiCallScriptedGenerator{events: [][]llm.Event{
		{
			{Kind: llm.EventText, TextDelta: "Searching..."},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"x"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: " done."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	env := newChatTestEnvWith(t, nil, chatTestOpts{
		mainGen:  gen,
		settings: rag.Settings{MaxToolRounds: 3},
	})

	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	body, _ := json.Marshal(map[string]string{"content": "find x"})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID.String()+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rw.Code, rw.Body.String())
	}

	frames := parseSSE(t, rw.Body.String())
	if !containsInOrder(frames, []string{"retrieving", "tool_start", "tool_result", "usage", "done"}) {
		t.Errorf("frames out of order: %+v", frames)
	}

	var startPayload map[string]any
	var resultPayload map[string]any
	for _, f := range frames {
		switch f.event {
		case "tool_start":
			_ = json.Unmarshal([]byte(f.data), &startPayload)
		case "tool_result":
			_ = json.Unmarshal([]byte(f.data), &resultPayload)
		}
	}
	if startPayload["name"] != "nexus_search" {
		t.Errorf("tool_start name = %v", startPayload["name"])
	}
	if !strings.Contains(startPayload["args"].(string), "query") {
		t.Errorf("tool_start args = %v", startPayload["args"])
	}
	if resultPayload["name"] != "nexus_search" {
		t.Errorf("tool_result name = %v", resultPayload["name"])
	}
	if _, ok := resultPayload["summary"].(string); !ok {
		t.Errorf("tool_result summary missing or not string: %v", resultPayload["summary"])
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
func (b *brokenRegistry) Models() []llm.ModelInfo              { return nil }
func (b *brokenRegistry) AllConfiguredModels() []llm.ModelInfo { return nil }

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
	router := NewRouter(st, sc, p, cm, em, rm, lm, NewRAGManager(st, zap.NewNop()), orchestrator, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())
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

	router := NewRouter(st, sc, p, cm, em, rm, lm, NewRAGManager(st, zap.NewNop()), nil, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())

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

	router := NewRouter(st, sc, p, cm, em, rm, lm, NewRAGManager(st, zap.NewNop()), nil /* no orchestrator */, sjm, nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())
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

// --- Phase 4 SSE drains: rewriter + skip-retrieval + auto-title ---

// streamMessage helper drives one POST /api/chats/:id/messages and
// returns the parsed SSE frame slice.
func streamMessage(t *testing.T, env chatTestEnv, chatID, content string) []sseFrame {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+chatID+"/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+env.ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	env.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("stream message status=%d body=%s", rw.Code, rw.Body.String())
	}
	return parseSSE(t, rw.Body.String())
}

func findFrame(frames []sseFrame, event string) (sseFrame, bool) {
	for _, f := range frames {
		if f.event == event {
			return f, true
		}
	}
	return sseFrame{}, false
}

func TestPostMessage_RewriterRewritesAndStreamsRetrievingFrame(t *testing.T) {
	// Two-turn flow: turn 1 (no rewriter), turn 2 (rewriter fires +
	// retrieving frame carries the rewritten query).
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5", BareID: "claude-haiku-4-5"}
	rewriter := rewriterScript("RETRIEVE: yes", "largest Anthropic invoice from April 2026")

	mainEvents := []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}
	env := newChatTestEnvWith(t, mainEvents, chatTestOpts{
		rewriterGen:  rewriter,
		rewriterInfo: rewriterInfo,
		settings:     rag.Settings{RewriterModel: rewriterInfo.ID},
	})

	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6", "title": "preset"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Turn 1.
	streamMessage(t, env, chat.ID.String(), "find Anthropic invoices")

	// Turn 2 — must surface the rewritten query in the retrieving frame.
	frames2 := streamMessage(t, env, chat.ID.String(), "which one was largest?")
	rf, ok := findFrame(frames2, "retrieving")
	if !ok {
		t.Fatalf("no retrieving frame in turn 2: %+v", frames2)
	}
	var retrieving struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(rf.data), &retrieving); err != nil {
		t.Fatalf("decode retrieving: %v", err)
	}
	if retrieving.Query != "largest Anthropic invoice from April 2026" {
		t.Errorf("retrieving.query=%q want rewritten", retrieving.Query)
	}

	// Persisted message carries the rewritten query.
	msgs, _ := env.st.ListMessages(context.Background(), chat.ID)
	turn2Asst := msgs[len(msgs)-1]
	if turn2Asst.RewrittenQuery != "largest Anthropic invoice from April 2026" {
		t.Errorf("persisted RewrittenQuery=%q", turn2Asst.RewrittenQuery)
	}
}

func TestPostMessage_SkipRetrieval_EmitsSkippedFrameNoEvidence(t *testing.T) {
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5", BareID: "claude-haiku-4-5"}
	rewriter := rewriterScript("RETRIEVE: no", "greeting")

	mainEvents := []llm.Event{
		{Kind: llm.EventText, TextDelta: "hello!"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}
	env := newChatTestEnvWith(t, mainEvents, chatTestOpts{
		rewriterGen:  rewriter,
		rewriterInfo: rewriterInfo,
		settings:     rag.Settings{RewriterModel: rewriterInfo.ID},
	})

	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6", "title": "preset"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)

	// Seed a prior assistant turn so the rewriter gate fires.
	streamMessage(t, env, chat.ID.String(), "first question")

	// Replace the main events for turn 2.
	// (The scripted generator has been drained; rebuild env with fresh
	// events isn't ideal but cleanest.)
	frames := streamMessage(t, env, chat.ID.String(), "thanks")
	if _, ok := findFrame(frames, "skipped_retrieval"); !ok {
		t.Errorf("expected skipped_retrieval frame: %+v", frames)
	}
	if _, ok := findFrame(frames, "retrieving"); ok {
		t.Errorf("retrieving frame should not appear on skipped turn")
	}
	if _, ok := findFrame(frames, "evidence"); ok {
		t.Errorf("evidence frame should not appear on skipped turn")
	}

	msgs, _ := env.st.ListMessages(context.Background(), chat.ID)
	turn2Asst := msgs[len(msgs)-1]
	if !turn2Asst.SkippedRetrieval {
		t.Errorf("persisted SkippedRetrieval=false; want true")
	}
}

func TestPostMessage_AutoTitle_FiresOnFirstEndTurnAndPrecedesDone(t *testing.T) {
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5", BareID: "claude-haiku-4-5"}
	titleGen := &scriptedGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: "Anthropic invoice summary"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
	mainEvents := []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}
	env := newChatTestEnvWith(t, mainEvents, chatTestOpts{
		rewriterGen:  titleGen,
		rewriterInfo: rewriterInfo,
		settings:     rag.Settings{RewriterModel: rewriterInfo.ID},
	})

	w := doChatRequest(t, env.router, http.MethodPost, "/api/chats", env.ownerToken,
		map[string]string{"default_model": "anthropic:claude-sonnet-4-6"})
	var chat model.Chat
	decodeChatData(t, w.Body, &chat)
	// Sanity: chat ships untitled.
	if chat.Title != "" {
		t.Fatalf("expected untitled chat at create, got %q", chat.Title)
	}

	frames := streamMessage(t, env, chat.ID.String(), "find Anthropic invoices")

	// title frame must appear, ordered before done.
	titleIdx, doneIdx := -1, -1
	for i, f := range frames {
		switch f.event {
		case "title":
			titleIdx = i
		case "done":
			doneIdx = i
		}
	}
	if titleIdx < 0 {
		t.Fatalf("no title frame: %+v", frames)
	}
	if titleIdx >= doneIdx {
		t.Errorf("title (%d) must precede done (%d)", titleIdx, doneIdx)
	}

	// chat row got the title persisted.
	loaded, err := env.st.GetChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if loaded.Title != "Anthropic invoice summary" {
		t.Errorf("chat.Title=%q", loaded.Title)
	}
}
