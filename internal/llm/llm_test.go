package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// --- catalog tests ---

func TestCatalog_HasAllProviders(t *testing.T) {
	c := Catalog()
	if len(c) == 0 {
		t.Fatal("empty catalog")
	}
	gotProviders := map[string]bool{}
	for _, m := range c {
		gotProviders[m.Provider] = true
		if m.ID == "" || m.Provider == "" || m.BareID == "" || m.DisplayName == "" {
			t.Errorf("entry has empty required fields: %+v", m)
		}
	}
	for _, want := range []string{ProviderAnthropic, ProviderOpenAI, ProviderOllama} {
		if !gotProviders[want] {
			t.Errorf("catalog missing provider %q", want)
		}
	}
}

func TestLookupModel(t *testing.T) {
	if _, ok := LookupModel("anthropic:claude-sonnet-4-6"); !ok {
		t.Error("expected sonnet 4.6 in catalog")
	}
	if _, ok := LookupModel("nope:nope"); ok {
		t.Error("did not expect lookup hit for missing id")
	}
}

func TestCatalogForProvider(t *testing.T) {
	a := CatalogForProvider(ProviderAnthropic)
	if len(a) == 0 {
		t.Fatal("expected anthropic models")
	}
	for _, m := range a {
		if m.Provider != ProviderAnthropic {
			t.Errorf("filter leak: %s", m.Provider)
		}
	}
}

// --- registry tests ---

type stubGen struct{ provider string }

func (s *stubGen) Generate(_ context.Context, _ GenerateRequest) (<-chan Event, error) {
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

func TestRegistry_GetUnknownProvider(t *testing.T) {
	r := NewRegistry(RegistryConfig{
		Generators: map[string]Generator{},
	})
	if _, _, err := r.Get("anthropic:claude-sonnet-4-6"); err == nil {
		t.Error("expected error for unconfigured provider")
	}
}

func TestRegistry_GetMalformedID(t *testing.T) {
	r := NewRegistry(RegistryConfig{Generators: map[string]Generator{ProviderAnthropic: &stubGen{}}})
	for _, id := range []string{"", "anthropic", ":foo", "anthropic:"} {
		if _, _, err := r.Get(id); err == nil {
			t.Errorf("expected error for malformed id %q", id)
		}
	}
}

func TestRegistry_GetUnknownModel(t *testing.T) {
	r := NewRegistry(RegistryConfig{Generators: map[string]Generator{ProviderAnthropic: &stubGen{}}})
	if _, _, err := r.Get("anthropic:does-not-exist"); err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestRegistry_GetCatalogModel(t *testing.T) {
	r := NewRegistry(RegistryConfig{Generators: map[string]Generator{ProviderAnthropic: &stubGen{provider: ProviderAnthropic}}})
	gen, info, err := r.Get("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if gen == nil {
		t.Error("nil generator")
	}
	if info.Provider != ProviderAnthropic {
		t.Errorf("provider = %q", info.Provider)
	}
}

func TestRegistry_AllowlistFiltersGet(t *testing.T) {
	r := NewRegistry(RegistryConfig{
		Generators: map[string]Generator{ProviderAnthropic: &stubGen{}},
		Allowlist:  []string{"anthropic:claude-haiku-4-5"},
	})
	if _, _, err := r.Get("anthropic:claude-sonnet-4-6"); err == nil {
		t.Error("expected sonnet to be blocked by allowlist")
	}
	if _, _, err := r.Get("anthropic:claude-haiku-4-5"); err != nil {
		t.Errorf("haiku should be allowed: %v", err)
	}
}

func TestRegistry_ModelsHidesUnconfiguredAndOutsideAllowlist(t *testing.T) {
	r := NewRegistry(RegistryConfig{
		Generators: map[string]Generator{ProviderAnthropic: &stubGen{}},
		Allowlist:  []string{"anthropic:claude-haiku-4-5"},
	})
	models := r.Models()
	if len(models) != 1 || models[0].ID != "anthropic:claude-haiku-4-5" {
		t.Errorf("models = %+v", models)
	}
}

func TestRegistry_AllConfiguredModelsIgnoresAllowlist(t *testing.T) {
	r := NewRegistry(RegistryConfig{
		Generators: map[string]Generator{ProviderAnthropic: &stubGen{}},
		Allowlist:  []string{"anthropic:claude-haiku-4-5"},
	})
	// Models() honours the allowlist (1); AllConfiguredModels() ignores it
	// and returns every configured-provider catalog model (all 3 Anthropic).
	if got := len(r.Models()); got != 1 {
		t.Fatalf("Models() = %d, want 1", got)
	}
	all := r.AllConfiguredModels()
	if len(all) <= 1 {
		t.Errorf("AllConfiguredModels() = %d, want > 1 (allowlist ignored)", len(all))
	}
}

func TestRegistry_ExtrasResolveOutsideCatalog(t *testing.T) {
	r := NewRegistry(RegistryConfig{
		Generators: map[string]Generator{ProviderOllama: &stubGen{}},
		Extras: []ModelInfo{{
			ID: "ollama:custom-local", Provider: ProviderOllama, BareID: "custom-local", DisplayName: "Custom",
		}},
	})
	if _, _, err := r.Get("ollama:custom-local"); err != nil {
		t.Errorf("extras lookup failed: %v", err)
	}
	models := r.Models()
	found := false
	for _, m := range models {
		if m.ID == "ollama:custom-local" {
			found = true
		}
	}
	if !found {
		t.Error("extras not surfaced via Models()")
	}
}

// --- error tests ---

func TestLLMError_Format(t *testing.T) {
	e := &LLMError{Provider: "anthropic", StatusCode: 500, Body: "oops"}
	if got := e.Error(); !strings.Contains(got, "500") || !strings.Contains(got, "oops") {
		t.Errorf("Error = %q", got)
	}
	bare := &LLMError{Provider: "openai", StatusCode: 401}
	if !strings.Contains(bare.Error(), "401") {
		t.Errorf("bare = %q", bare.Error())
	}
}

func TestLLMError_IsRetryable(t *testing.T) {
	for _, sc := range []int{429, 500, 502, 503, 504} {
		if !(&LLMError{StatusCode: sc}).IsRetryable() {
			t.Errorf("status %d should be retryable", sc)
		}
	}
	for _, sc := range []int{400, 401, 403, 404} {
		if (&LLMError{StatusCode: sc}).IsRetryable() {
			t.Errorf("status %d should NOT be retryable", sc)
		}
	}
}

func TestErrorFromResponse_CapsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL) //nolint:noctx,bodyclose // test
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	e := ErrorFromResponse(resp, "test")
	if len(e.Body) > 1024 {
		t.Errorf("body not capped, got %d bytes", len(e.Body))
	}
}

// --- retry tests ---

type flakyGen struct {
	calls   int
	failN   int
	failErr error
}

func (g *flakyGen) Generate(_ context.Context, _ GenerateRequest) (<-chan Event, error) {
	g.calls++
	if g.calls <= g.failN {
		return nil, g.failErr
	}
	ch := make(chan Event, 1)
	ch <- Event{Kind: EventDone, StopReason: StopEnd}
	close(ch)
	return ch, nil
}

func TestRetry_RetriesOnRetryableThenSucceeds(t *testing.T) {
	g := &flakyGen{failN: 2, failErr: &LLMError{StatusCode: 500}}
	r := &RetryGenerator{inner: g, maxRetries: 3, baseDelay: time.Microsecond, log: zap.NewNop()}
	ch, err := r.Generate(context.Background(), GenerateRequest{Model: "x"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for range ch {
		// drain the event channel until it closes
	}
	if g.calls != 3 {
		t.Errorf("calls = %d, want 3", g.calls)
	}
}

func TestRetry_StopsOnNonRetryable(t *testing.T) {
	g := &flakyGen{failN: 5, failErr: &LLMError{StatusCode: 401}}
	r := &RetryGenerator{inner: g, maxRetries: 3, baseDelay: time.Microsecond, log: zap.NewNop()}
	_, err := r.Generate(context.Background(), GenerateRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if g.calls != 1 {
		t.Errorf("calls = %d, want 1", g.calls)
	}
}

func TestRetry_StopsOnMaxAttempts(t *testing.T) {
	g := &flakyGen{failN: 99, failErr: &LLMError{StatusCode: 500}}
	r := &RetryGenerator{inner: g, maxRetries: 2, baseDelay: time.Microsecond, log: zap.NewNop()}
	_, err := r.Generate(context.Background(), GenerateRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if g.calls != 3 {
		t.Errorf("calls = %d, want 3 (1 initial + 2 retries)", g.calls)
	}
}

func TestRetry_NeverRetriesContextCancel(t *testing.T) {
	g := &flakyGen{failN: 99, failErr: context.Canceled}
	r := &RetryGenerator{inner: g, maxRetries: 3, baseDelay: time.Microsecond, log: zap.NewNop()}
	_, err := r.Generate(context.Background(), GenerateRequest{Model: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if g.calls != 1 {
		t.Errorf("calls = %d, want 1", g.calls)
	}
}

func TestNewRetryGenerator_Defaults(t *testing.T) {
	g := &flakyGen{}
	r := NewRetryGenerator(g, zap.NewNop())
	if r.maxRetries != defaultMaxRetries || r.baseDelay != defaultBaseDelay {
		t.Errorf("defaults = (%d, %s)", r.maxRetries, r.baseDelay)
	}
}

// --- multi-modal collection helpers (Phase 6/6b) ---

func TestCollectImagesAndPDFs(t *testing.T) {
	docs := []Document{
		{ID: "a", Images: []Image{{MediaType: "image/png", Data: []byte("1")}}},
		{ID: "b"},
		{ID: "c",
			Images: []Image{{MediaType: "image/jpeg", Data: []byte("2")}},
			PDFs:   []PDF{{Filename: "x.pdf", Data: []byte("p")}},
		},
	}
	if imgs := CollectImages(docs); len(imgs) != 2 {
		t.Errorf("CollectImages = %d, want 2", len(imgs))
	}
	pdfs := CollectPDFs(docs)
	if len(pdfs) != 1 || pdfs[0].Filename != "x.pdf" {
		t.Errorf("CollectPDFs = %+v, want one x.pdf", pdfs)
	}
	if len(CollectImages(nil)) != 0 || len(CollectPDFs(nil)) != 0 {
		t.Error("nil docs should collect nothing")
	}
}

func TestLastUserIndex(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem}, {Role: RoleUser}, {Role: RoleAssistant}, {Role: RoleUser},
	}
	if got := LastUserIndex(msgs); got != 3 {
		t.Errorf("LastUserIndex = %d, want 3", got)
	}
	if got := LastUserIndex([]Message{{Role: RoleAssistant}}); got != -1 {
		t.Errorf("no user → %d, want -1", got)
	}
	if got := LastUserIndex(nil); got != -1 {
		t.Errorf("nil → %d, want -1", got)
	}
}
