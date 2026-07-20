package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/search"
	"go.uber.org/zap"
)

type mockConnector struct {
	name     string
	typ      string
	fetchErr error
}

func (m *mockConnector) Type() string                       { return m.typ }
func (m *mockConnector) Name() string                       { return m.name }
func (m *mockConnector) Configure(_ connector.Config) error { return nil }
func (m *mockConnector) Validate() error                    { return nil }

func (m *mockConnector) Fetch(_ context.Context, _ *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)
	if m.fetchErr != nil {
		errs <- m.fetchErr
	}
	close(items)
	close(errs)
	return items, errs
}

func newTestHandler() *handler {
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	cm := &ConnectorManager{
		connectors: map[uuid.UUID]*connectorEntry{
			testID: {
				conn:   &mockConnector{name: "test-fs", typ: "filesystem"},
				config: model.ConnectorConfig{ID: testID, Name: "test-fs", Type: "filesystem", Enabled: true, Shared: true},
			},
		},
		log: zap.NewNop(),
	}
	return &handler{
		cm:        cm,
		rm:        NewRerankManager(nil, zap.NewNop()),
		syncJobs:  NewSyncJobManager(nil, zap.NewNop()),
		pipeline:  pipeline.New(nil, nil, nil, zap.NewNop()),
		jwtSecret: []byte("test-secret"),
		log:       zap.NewNop(),
	}
}

// withAdminContext attaches admin claims to a request so handlers that rely on
// auth context (canRead/canModify) work in unit tests.
func withAdminContext(req *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:           uuid.New(),
		Username:         "test-admin",
		Role:             "admin",
		RegisteredClaims: jwt.RegisteredClaims{},
	}
	return req.WithContext(auth.ContextWithClaims(req.Context(), claims))
}

func TestHealthHandler(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	h.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

// TestHealthReadyHandler_NilDeps covers the nil-guard branch: with no store or
// search wired (newTestHandler leaves both nil), readiness reports 200 "ok"
// with an empty component map rather than panicking.
func TestHealthReadyHandler_NilDeps(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
	w := httptest.NewRecorder()

	h.HealthReady(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with nil deps, got %d", w.Code)
	}
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map data, got %T", resp.Data)
	}
	if data["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", data["status"])
	}
	if comps, ok := data["components"].(map[string]any); !ok || len(comps) != 0 {
		t.Errorf("expected empty components map, got %v", data["components"])
	}
}

// TestConnectorResponse_SurfacesCredentialsUnreadable locks the wire contract:
// a connector whose secrets couldn't be decrypted must expose
// credentials_unreadable through the connectors API so the UI can prompt the
// owner to re-enter the secret.
func TestConnectorResponse_SurfacesCredentialsUnreadable(t *testing.T) {
	resp := connectorResponse{
		ConnectorConfig: model.ConnectorConfig{
			Type:                  "imap",
			Name:                  "unreadable",
			Config:                map[string]any{"host": "mail.example.com"},
			CredentialsUnreadable: true,
		},
		Status: "inactive",
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"credentials_unreadable":true`) {
		t.Errorf("expected credentials_unreadable in JSON, got %s", b)
	}
}

func TestSearchHandler_MissingQuery(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	w := httptest.NewRecorder()

	h.Search(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected error message for missing query")
	}
}

func TestTriggerSyncHandler_NotFound(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Post("/api/sync/{id}", h.TriggerSync)

	req := withAdminContext(httptest.NewRequest(http.MethodPost, "/api/sync/"+uuid.New().String(), nil))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestTriggerSyncHandler_InvalidID(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Post("/api/sync/{id}", h.TriggerSync)

	req := withAdminContext(httptest.NewRequest(http.MethodPost, "/api/sync/not-a-uuid", nil))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestTriggerSyncHandler_Accepted(t *testing.T) {
	h := newTestHandler()
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	r := chi.NewRouter()
	r.Post("/api/sync/{id}", h.TriggerSync)

	req := withAdminContext(httptest.NewRequest(http.MethodPost, "/api/sync/"+testID.String(), nil))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", w.Code)
	}

	var resp struct {
		Data *SyncJob `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected sync job in response")
	}
	if resp.Data.Status != "running" {
		t.Errorf("status = %q, want running", resp.Data.Status)
	}
	if resp.Data.ConnectorName != "test-fs" {
		t.Errorf("connector_name = %q, want test-fs", resp.Data.ConnectorName)
	}
}

func TestTriggerSyncHandler_Conflict(t *testing.T) {
	h := newTestHandler()
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if _, _, err := h.syncJobs.Start(testID, "test-fs", "filesystem"); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/api/sync/{id}", h.TriggerSync)

	req := withAdminContext(httptest.NewRequest(http.MethodPost, "/api/sync/"+testID.String(), nil))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

func TestListSyncJobsHandler(t *testing.T) {
	h := newTestHandler()
	// ListSyncJobs filters jobs by connector visibility, so we add two extra
	// shared connectors to the manager and start jobs against them.
	id1, id2 := uuid.New(), uuid.New()
	h.cm.connectors[id1] = &connectorEntry{
		conn:   &mockConnector{name: "a", typ: "filesystem"},
		config: model.ConnectorConfig{ID: id1, Name: "a", Type: "filesystem", Shared: true},
	}
	h.cm.connectors[id2] = &connectorEntry{
		conn:   &mockConnector{name: "b", typ: "imap"},
		config: model.ConnectorConfig{ID: id2, Name: "b", Type: "imap", Shared: true},
	}
	if _, _, err := h.syncJobs.Start(id1, "a", "filesystem"); err != nil {
		t.Fatalf("seed job a: %v", err)
	}
	if _, _, err := h.syncJobs.Start(id2, "b", "imap"); err != nil {
		t.Fatalf("seed job b: %v", err)
	}

	req := withAdminContext(httptest.NewRequest(http.MethodGet, "/api/sync", nil))
	w := httptest.NewRecorder()

	h.ListSyncJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Data []*SyncJob `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("got %d jobs, want 2", len(resp.Data))
	}
}

func TestGetConnectorHandler_InvalidID(t *testing.T) {
	h := newTestHandler()
	r := chi.NewRouter()
	r.Get("/api/connectors/{id}", h.GetConnector)
	req := withAdminContext(httptest.NewRequest(http.MethodGet, "/api/connectors/not-a-uuid", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateConnectorHandler_InvalidID(t *testing.T) {
	h := newTestHandler()
	r := chi.NewRouter()
	r.Put("/api/connectors/{id}", h.UpdateConnector)
	req := withAdminContext(httptest.NewRequest(http.MethodPut, "/api/connectors/not-a-uuid", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteConnectorHandler_InvalidID(t *testing.T) {
	h := newTestHandler()
	r := chi.NewRouter()
	r.Delete("/api/connectors/{id}", h.DeleteConnector)
	req := withAdminContext(httptest.NewRequest(http.MethodDelete, "/api/connectors/not-a-uuid", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestStreamSyncProgressHandler_InvalidID(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Get("/api/sync/{id}/progress", h.StreamSyncProgress)

	req := withAdminContext(httptest.NewRequest(http.MethodGet, "/api/sync/not-a-uuid/progress", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestStreamSyncProgressHandler_NoActiveSync(t *testing.T) {
	h := newTestHandler()
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	r := chi.NewRouter()
	r.Get("/api/sync/{id}/progress", h.StreamSyncProgress)

	// Connector exists but no sync job is running for it
	req := withAdminContext(httptest.NewRequest(http.MethodGet, "/api/sync/"+testID.String()+"/progress", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no active sync, got %d", w.Code)
	}
}

func TestDeleteCursorHandler_InvalidID(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Delete("/api/sync/cursors/{id}", h.DeleteCursor)

	req := withAdminContext(httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/bad-uuid", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteCursorHandler_NotFound(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Delete("/api/sync/cursors/{id}", h.DeleteCursor)

	req := withAdminContext(httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/"+uuid.New().String(), nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- canRead/canModify helper edge cases ---

func TestCanReadConnector_NilClaims(t *testing.T) {
	if canReadConnector(nil, &model.ConnectorConfig{Shared: true}) {
		t.Error("nil claims should never be allowed to read")
	}
}

func TestCanModifyConnector_NilClaims(t *testing.T) {
	if canModifyConnector(nil, &model.ConnectorConfig{Shared: true}) {
		t.Error("nil claims should never be allowed to modify")
	}
}

func TestCanReadConnector_NonAdminPrivateConnectorWithoutOwner(t *testing.T) {
	// A private connector with no owner should not be readable by a non-admin user
	claims := &auth.Claims{UserID: uuid.New(), Role: "user"}
	cfg := &model.ConnectorConfig{Shared: false, UserID: nil}
	if canReadConnector(claims, cfg) {
		t.Error("non-admin user should not read private connector with no owner")
	}
}

func TestSyncAllHandler(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	w := httptest.NewRecorder()

	h.SyncAll(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

type mockReranker struct {
	results []rerank.Result
}

func (m *mockReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.Result, error) {
	if m.results != nil {
		return m.results, nil
	}
	// Reverse order by default
	results := make([]rerank.Result, len(docs))
	for i := range docs {
		results[i] = rerank.Result{Index: len(docs) - 1 - i, Score: float64(len(docs)-i) * 0.1}
	}
	return results, nil
}

func TestRerankResults(t *testing.T) {
	rm := NewRerankManager(nil, zap.NewNop())
	rm.Set(&mockReranker{
		results: []rerank.Result{
			{Index: 2, Score: 0.9},
			{Index: 0, Score: 0.5},
			{Index: 1, Score: 0.3},
		},
	})

	s := &SearchService{rm: rm, log: zap.NewNop()}

	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "A"}, Rank: 1.0},
			{Document: model.Document{Title: "B"}, Rank: 0.8},
			{Document: model.Document{Title: "C"}, Rank: 0.6},
		},
	}

	reranked, ok := s.rerankResults(context.Background(), "test", result)
	if !ok {
		t.Fatal("expected reranked=true when a reranker rescores >1 candidate")
	}

	if len(reranked.Documents) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(reranked.Documents))
	}
	if reranked.Documents[0].Title != "C" {
		t.Errorf("first result should be C (highest rerank score), got %q", reranked.Documents[0].Title)
	}
	if reranked.Documents[0].Rank != 0.9 {
		t.Errorf("first result score = %f, want 0.9", reranked.Documents[0].Rank)
	}
}

func TestRerankResults_NoReranker(t *testing.T) {
	rm := NewRerankManager(nil, zap.NewNop())
	s := &SearchService{rm: rm, log: zap.NewNop()}

	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "A"}},
		},
	}

	reranked, ok := s.rerankResults(context.Background(), "test", result)
	if ok {
		t.Error("expected reranked=false when no reranker is wired")
	}
	if reranked.Documents[0].Title != "A" {
		t.Error("should return original order when no reranker")
	}
}

// recordingReranker captures the texts it received so a test can verify which
// docs reached the reranker.
type recordingReranker struct{ received []string }

func (r *recordingReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.Result, error) {
	r.received = append(r.received, docs...)
	results := make([]rerank.Result, len(docs))
	for i := range docs {
		results[i] = rerank.Result{Index: i, Score: 1.0}
	}
	return results, nil
}

func TestRerankResults_DedupesNearDuplicates(t *testing.T) {
	rec := &recordingReranker{}
	rm := NewRerankManager(nil, zap.NewNop())
	rm.Set(rec)
	s := &SearchService{rm: rm, log: zap.NewNop()}

	// Three docs: 1 and 3 are exact duplicates (same title + same first 200
	// chars of content). 2 is unique. After dedup, only 2 should reach the
	// reranker.
	dup := strings.Repeat("identical newsletter prefix that is more than two hundred characters long ", 5)
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Hello Developer", Content: dup}, Rank: 0.9},
			{Document: model.Document{Title: "Different Doc", Content: "totally different content here"}, Rank: 0.8},
			{Document: model.Document{Title: "Hello Developer", Content: dup}, Rank: 0.7},
		},
	}

	reranked, ok := s.rerankResults(context.Background(), "test", result)
	if !ok {
		t.Error("expected reranked=true after a successful rerank")
	}
	if len(rec.received) != 2 {
		t.Errorf("expected reranker to receive 2 deduped docs, got %d", len(rec.received))
	}
	if len(reranked.Documents) != 2 {
		t.Errorf("expected 2 docs after dedup, got %d", len(reranked.Documents))
	}
}

// errReranker always fails, to exercise the degraded "kept original order" path.
type errReranker struct{}

func (errReranker) Rerank(context.Context, string, []string) ([]rerank.Result, error) {
	return nil, errors.New("reranker unavailable")
}

// TestRerankResults_ReportsWhetherReranked pins the bool that gates the score
// floor. It must be false in every path where Documents still carry raw
// retrieval (RRF/BM25) scores — no reranker, single candidate, or API error —
// and true only when the reranker actually rescored. Regression for the floor
// wiping raw-RRF results.
func TestRerankResults_ReportsWhetherReranked(t *testing.T) {
	mk := func(n int) *model.SearchResult {
		docs := make([]model.DocumentHit, n)
		for i := range docs {
			docs[i] = model.DocumentHit{Document: model.Document{Title: string(rune('A' + i))}, Rank: 0.03}
		}
		return &model.SearchResult{Documents: docs}
	}

	cases := []struct {
		name     string
		reranker rerank.Reranker
		docs     int
		want     bool
	}{
		{"no reranker", nil, 3, false},
		{"single candidate", &mockReranker{}, 1, false},
		{"reranker error", errReranker{}, 3, false},
		{"healthy rerank", &mockReranker{}, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rm := NewRerankManager(nil, zap.NewNop())
			if tc.reranker != nil {
				rm.Set(tc.reranker)
			}
			s := &SearchService{rm: rm, log: zap.NewNop()}
			if _, ok := s.rerankResults(context.Background(), "q", mk(tc.docs)); ok != tc.want {
				t.Errorf("reranked=%v, want %v", ok, tc.want)
			}
		})
	}
}

// TestApplyRerankerFloor_SkippedWhenNotReranked verifies the score floor only
// culls real reranker scores. Raw RRF scores (~0.03) are far below the 0.4
// floor, so applying it to un-reranked results would empty the response.
func TestApplyRerankerFloor_SkippedWhenNotReranked(t *testing.T) {
	cfg := search.DefaultRankingConfig() // RerankerMinScore = 0.4
	mk := func() *model.SearchResult {
		return &model.SearchResult{Documents: []model.DocumentHit{
			{Document: model.Document{Title: "A"}, Rank: 0.033},
			{Document: model.Document{Title: "B"}, Rank: 0.016},
		}}
	}

	notReranked := mk()
	applyRerankerFloor(notReranked, cfg, false)
	if len(notReranked.Documents) != 2 {
		t.Errorf("floor must not run on raw-RRF (un-reranked) results; got %d docs, want 2", len(notReranked.Documents))
	}

	reranked := mk()
	applyRerankerFloor(reranked, cfg, true)
	if len(reranked.Documents) != 0 {
		t.Errorf("floor should drop sub-threshold reranked scores; got %d docs, want 0", len(reranked.Documents))
	}
}

func TestDedupeNearDuplicates_KeepsFirst(t *testing.T) {
	docs := []model.DocumentHit{
		{Document: model.Document{Title: "X", Content: "same content"}, Rank: 0.9},
		{Document: model.Document{Title: "X", Content: "same content"}, Rank: 0.5},
		{Document: model.Document{Title: "Y", Content: "different content"}, Rank: 0.4},
	}
	out := dedupeNearDuplicates(docs)
	if len(out) != 2 {
		t.Fatalf("expected 2 docs after dedup, got %d", len(out))
	}
	if out[0].Rank != 0.9 {
		t.Errorf("expected first occurrence (rank 0.9) to win, got rank %f", out[0].Rank)
	}
}

func TestDedupeNearDuplicates_PreservesUnique(t *testing.T) {
	docs := []model.DocumentHit{
		{Document: model.Document{Title: "A", Content: "alpha"}},
		{Document: model.Document{Title: "B", Content: "beta"}},
		{Document: model.Document{Title: "C", Content: "gamma"}},
	}
	out := dedupeNearDuplicates(docs)
	if len(out) != 3 {
		t.Errorf("expected 3 unique docs preserved, got %d", len(out))
	}
}

func TestReorderByRerankScores(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "First"}},
			{Document: model.Document{Title: "Second"}},
		},
	}

	ranked := []rerank.Result{
		{Index: 1, Score: 0.9},
		{Index: 0, Score: 0.1},
	}

	reordered := reorderByRerankScores(result, ranked)
	if reordered.Documents[0].Title != "Second" {
		t.Errorf("expected Second first, got %q", reordered.Documents[0].Title)
	}
}

func TestParseCSV(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"paperless", 1},
		{"paperless,filesystem", 2},
		{"paperless, filesystem , ", 2},
	}
	for _, tt := range tests {
		result := parseCSV(tt.input)
		if len(result) != tt.want {
			t.Errorf("parseCSV(%q) = %d items, want %d", tt.input, len(result), tt.want)
		}
	}
}
