package rag

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// fakeChats is an in-memory ChatStore for orchestrator tests. It captures
// chats, message history, and any title updates so test cases can assert
// on persistence side effects without touching Postgres.
type fakeChats struct {
	mu       sync.Mutex
	chats    map[uuid.UUID]*model.Chat
	messages map[uuid.UUID][]model.ChatMessage
	updates  []store.ChatUpdate // appended on each UpdateChat call
}

func newFakeChats() *fakeChats {
	return &fakeChats{
		chats:    map[uuid.UUID]*model.Chat{},
		messages: map[uuid.UUID][]model.ChatMessage{},
	}
}

func (f *fakeChats) seed(c *model.Chat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chats[c.ID] = c
}

func (f *fakeChats) GetChat(_ context.Context, id uuid.UUID) (*model.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.chats[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (f *fakeChats) ListMessages(_ context.Context, chatID uuid.UUID) ([]model.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.ChatMessage, len(f.messages[chatID]))
	copy(out, f.messages[chatID])
	return out, nil
}

func (f *fakeChats) AppendMessage(_ context.Context, msg *model.ChatMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.chats[msg.ChatID]; !ok {
		return errors.New("chat not found")
	}
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	msg.Seq = len(f.messages[msg.ChatID]) + 1
	f.messages[msg.ChatID] = append(f.messages[msg.ChatID], *msg)
	return nil
}

func (f *fakeChats) UpdateChat(_ context.Context, id uuid.UUID, fields store.ChatUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, fields)
	c, ok := f.chats[id]
	if !ok {
		return store.ErrNotFound
	}
	if fields.Title != nil {
		c.Title = *fields.Title
	}
	if fields.DefaultModel != nil {
		c.DefaultModel = *fields.DefaultModel
	}
	return nil
}

// titleUpdates returns the slice of titles applied via UpdateChat. Returns
// only non-nil Title fields in order — handy for asserting that an
// auto-title path fired with the expected string.
func (f *fakeChats) titleUpdates() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, u := range f.updates {
		if u.Title != nil {
			out = append(out, *u.Title)
		}
	}
	return out
}

// fakeSearch is a stub SearchProvider returning a fixed slice of hits or
// a fixed error. Captures the most recent SearchRequest so tests can
// assert which query reached retrieval (e.g. the rewriter's output).
type fakeSearch struct {
	mu      sync.Mutex
	docs    []model.DocumentHit
	err     error
	lastReq model.SearchRequest
	calls   int
}

func (f *fakeSearch) Run(_ context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	f.mu.Lock()
	f.lastReq = req
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &model.SearchResult{Documents: f.docs, TotalCount: len(f.docs)}, nil
}

func (f *fakeSearch) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSearch) lastRequest() model.SearchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// fakeGenerator scripts a sequence of llm.Events. Setting `delay` makes
// each event wait that long before being sent, so cancellation tests can
// interrupt mid-stream. Captures every inbound GenerateRequest so
// multi-round tests can assert on per-call Documents / Messages /
// Tools composition.
//
// Phase 5 multi-round support: when `eventsPerCall` is non-empty, each
// successive Generate call emits the next inner slice (clamped to the
// last entry on overflow so a misbehaving model can't OOB the test).
// When unset, every call emits the same `events` slice (Phase 4
// behaviour).
type fakeGenerator struct {
	mu            sync.Mutex
	events        []llm.Event
	eventsPerCall [][]llm.Event
	delay         time.Duration
	err           error
	requests      []llm.GenerateRequest
	calls         int
}

func (f *fakeGenerator) Generate(ctx context.Context, req llm.GenerateRequest) (<-chan llm.Event, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	callIdx := f.calls
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}

	events := f.events
	if len(f.eventsPerCall) > 0 {
		idx := callIdx
		if idx >= len(f.eventsPerCall) {
			idx = len(f.eventsPerCall) - 1
		}
		events = f.eventsPerCall[idx]
	}

	out := make(chan llm.Event, len(events))
	go func() {
		defer close(out)
		for _, ev := range events {
			if f.delay > 0 {
				select {
				case <-ctx.Done():
					out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
					return
				case <-time.After(f.delay):
				}
			}
			select {
			case <-ctx.Done():
				out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}

func (f *fakeGenerator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// lastRequest returns the most recent GenerateRequest the orchestrator
// dispatched. Callers that need every request use requestAt.
func (f *fakeGenerator) lastRequest() llm.GenerateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return llm.GenerateRequest{}
	}
	return f.requests[len(f.requests)-1]
}

// requestAt returns the i-th GenerateRequest (0-based). Callers assert
// per-round Documents / Tools composition.
func (f *fakeGenerator) requestAt(i int) llm.GenerateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.requests) {
		return llm.GenerateRequest{}
	}
	return f.requests[i]
}

// fakeRegistry returns a single Generator/ModelInfo pair. Optional
// rewriterGen lets a test plug a different generator under the rewriter
// model id without rebuilding the registry.
type fakeRegistry struct {
	gen          llm.Generator
	info         llm.ModelInfo
	err          error
	rewriterGen  llm.Generator
	rewriterInfo llm.ModelInfo
	rewriterErr  error
}

func (f *fakeRegistry) Get(id string) (llm.Generator, llm.ModelInfo, error) {
	if f.rewriterGen != nil && id == f.rewriterInfo.ID {
		return f.rewriterGen, f.rewriterInfo, f.rewriterErr
	}
	return f.gen, f.info, f.err
}

func (f *fakeRegistry) Models() []llm.ModelInfo {
	if f.err != nil {
		return nil
	}
	out := []llm.ModelInfo{f.info}
	if f.rewriterGen != nil {
		out = append(out, f.rewriterInfo)
	}
	return out
}

func (f *fakeRegistry) AllConfiguredModels() []llm.ModelInfo {
	return f.Models()
}

// failingChatStore wraps fakeChats but returns an error from
// ListMessages. Used to exercise the orchestrator's history-pack
// failure-is-non-fatal branch.
type failingChatStore struct{ *fakeChats }

func (f *failingChatStore) ListMessages(_ context.Context, _ uuid.UUID) ([]model.ChatMessage, error) {
	return nil, errors.New("list-msgs failure")
}

// errChatStore returns a configurable error from GetChat or AppendMessage.
type errChatStore struct {
	*fakeChats
	getErr    error
	appendErr error
}

func (e *errChatStore) GetChat(ctx context.Context, id uuid.UUID) (*model.Chat, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.fakeChats.GetChat(ctx, id)
}

func (e *errChatStore) AppendMessage(ctx context.Context, msg *model.ChatMessage) error {
	if e.appendErr != nil {
		return e.appendErr
	}
	return e.fakeChats.AppendMessage(ctx, msg)
}

// --- helpers ---

// newOrchTest wires a default-config Orchestrator with the supplied
// generator + search + chats. Empty SettingsFunc means "rewriter
// disabled / default model resolution from chat".
func newOrchTest(t *testing.T, gen *fakeGenerator, info llm.ModelInfo, search SearchProvider) (*Orchestrator, *fakeChats) {
	t.Helper()
	chats := newFakeChats()
	reg := &fakeRegistry{gen: gen, info: info}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return reg },
		Settings: func() Settings { return Settings{} },
		Search:   search,
		Chats:    chats,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	return o, chats
}

// newOrchTestWithRewriter wires an Orchestrator that exposes a separate
// rewriter generator under a configured rewriter model id. The Settings
// closure returns the rewriter id so the orchestrator can dispatch the
// rewriter call.
func newOrchTestWithRewriter(t *testing.T, mainGen, rewriterGen *fakeGenerator, mainInfo, rewriterInfo llm.ModelInfo, search SearchProvider) (*Orchestrator, *fakeChats, *fakeRegistry) {
	t.Helper()
	chats := newFakeChats()
	reg := &fakeRegistry{
		gen:          mainGen,
		info:         mainInfo,
		rewriterGen:  rewriterGen,
		rewriterInfo: rewriterInfo,
	}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return reg },
		Settings: func() Settings { return Settings{RewriterModel: rewriterInfo.ID} },
		Search:   search,
		Chats:    chats,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	return o, chats, reg
}

// runOrchAndDrain runs Run with the supplied input and drains the
// returned event channel into a slice. Fails the test on a sync error
// from Run or a 2-second collection timeout.
func runOrchAndDrain(t *testing.T, o *Orchestrator, in RunInput) []Event {
	t.Helper()
	ch, err := o.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return collectEvents(t, ch, 2*time.Second)
}

func collectEvents(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out collecting events; got %d so far", len(events))
		}
	}
}

func makeChat(t *testing.T, chats *fakeChats, defaultModel string) *model.Chat {
	t.Helper()
	c := &model.Chat{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		DefaultModel: defaultModel,
	}
	chats.seed(c)
	return c
}
