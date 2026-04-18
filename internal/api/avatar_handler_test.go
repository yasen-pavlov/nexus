//go:build integration

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// newRouterWithBinaryStore builds a router wired to a real filesystem
// binary store so avatar-endpoint tests can round-trip through Get.
func newRouterWithBinaryStore(t *testing.T) (*store.Store, *storage.BinaryStore, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	bs, err := storage.New(t.TempDir(), st, zap.NewNop())
	if err != nil {
		t.Fatalf("create binary store: %v", err)
	}
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), bs, testJWTSecret, nil, zap.NewNop())
	return st, bs, router
}

func TestGetConnectorAvatar_StreamsCachedImage(t *testing.T) {
	st, bs, router := newRouterWithBinaryStore(t)
	userID, token := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-test",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
		ExternalID: "9001", ExternalName: "Me",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}

	payload := []byte("fake-jpeg-bytes")
	if err := bs.Put(ctx, "telegram", "tg-test", "avatars:9001",
		bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("seed binary store: %v", err)
	}

	url := "/api/connectors/" + cfg.ID.String() + "/avatars/9001"
	w := doJSON(t, router, http.MethodGet, url, "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	body, _ := io.ReadAll(w.Body)
	if !bytes.Equal(body, payload) {
		t.Errorf("body mismatch")
	}
}

func TestGetConnectorAvatar_404WhenUncached(t *testing.T) {
	st, _, router := newRouterWithBinaryStore(t)
	userID, token := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-uncached",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
		ExternalID: "9002",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}

	url := "/api/connectors/" + cfg.ID.String() + "/avatars/9002"
	w := doJSON(t, router, http.MethodGet, url, "", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetConnectorAvatar_404ForNonOwner(t *testing.T) {
	st, bs, router := newRouterWithBinaryStore(t)
	ownerID, _ := createTestUser(t, st)
	_, otherToken := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-private",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &ownerID,
		ExternalID: "1",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}
	_ = bs.Put(ctx, "telegram", "tg-private", "avatars:1",
		strings.NewReader("x"), 1)

	url := "/api/connectors/" + cfg.ID.String() + "/avatars/1"
	w := doJSON(t, router, http.MethodGet, url, "", otherToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner, got %d", w.Code)
	}
}

func TestGetConnectorAvatar_404ForUnsupportedSource(t *testing.T) {
	st, _, router := newRouterWithBinaryStore(t)
	userID, token := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "fs-no-avatars",
		Config:  map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, UserID: &userID,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}

	url := "/api/connectors/" + cfg.ID.String() + "/avatars/anything"
	w := doJSON(t, router, http.MethodGet, url, "", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unsupported source, got %d", w.Code)
	}
}

func TestGetConnectorAvatar_400OnInvalidConnectorID(t *testing.T) {
	st, _, router := newRouterWithBinaryStore(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet, "/api/connectors/not-a-uuid/avatars/1", "", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetConnectorAvatar_404OnUnknownConnector(t *testing.T) {
	st, _, router := newRouterWithBinaryStore(t)
	_, token := createTestUser(t, st)

	// Valid UUID that doesn't exist in the DB.
	w := doJSON(t, router, http.MethodGet,
		"/api/connectors/00000000-0000-0000-0000-000000000000/avatars/1",
		"", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown connector, got %d", w.Code)
	}
}

func TestGetConnectorAvatar_404WhenBinaryStoreUnwired(t *testing.T) {
	// Build a router WITHOUT a binary store to cover the h.binaryStore
	// == nil guard. The endpoint should gracefully report "not cached"
	// rather than panic.
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em,
		NewRerankManager(st, zap.NewNop()), NewSyncJobManager(),
		nil, // ← no binary store
		testJWTSecret, nil, zap.NewNop())

	userID, token := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-no-store",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
		ExternalID: "9001",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}

	url := "/api/connectors/" + cfg.ID.String() + "/avatars/9001"
	w := doJSON(t, router, http.MethodGet, url, "", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when binaryStore is nil, got %d", w.Code)
	}
}
