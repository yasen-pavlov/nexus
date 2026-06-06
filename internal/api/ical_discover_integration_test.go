//go:build integration

package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/muty/nexus/internal/connector/ical" // register the ical connector for discovery
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

func newDiscoverTestRouter(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewLLMManager(st, zap.NewNop()), NewRAGManager(st, zap.NewNop()), nil, sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())
	return st, router
}

func discoverReq(router http.Handler, body, bearer string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/discover", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

func TestDiscover_Unauthenticated(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	createTestAdmin(t, st)
	if code := discoverReq(router, `{"type":"ical","config":{}}`, ""); code != http.StatusUnauthorized {
		t.Errorf("no token: expected 401, got %d", code)
	}
}

func TestDiscover_BadBody(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	if code := discoverReq(router, "not json", jwt); code != http.StatusBadRequest {
		t.Errorf("bad body: expected 400, got %d", code)
	}
}

func TestDiscover_MissingType(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	if code := discoverReq(router, `{"config":{}}`, jwt); code != http.StatusBadRequest {
		t.Errorf("missing type: expected 400, got %d", code)
	}
}

func TestDiscover_UnknownType(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	if code := discoverReq(router, `{"type":"nope","config":{}}`, jwt); code != http.StatusBadRequest {
		t.Errorf("unknown type: expected 400, got %d", code)
	}
}

// TestDiscover_UnsupportedType verifies a connector type that can't discover
// returns 400 (exercised without any network call — filesystem configures
// purely locally and doesn't implement ResourceDiscoverer).
func TestDiscover_UnsupportedType(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	body := `{"type":"filesystem","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"}}`
	if code := discoverReq(router, body, jwt); code != http.StatusBadRequest {
		t.Errorf("unsupported type: expected 400, got %d", code)
	}
}

// TestDiscover_ConfigError verifies a missing required credential is a 400
// (ical Configure rejects an empty username before any network call).
func TestDiscover_ConfigError(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	if code := discoverReq(router, `{"type":"ical","config":{}}`, jwt); code != http.StatusBadRequest {
		t.Errorf("config error: expected 400, got %d", code)
	}
}

// TestDiscover_UpstreamError verifies an unreachable endpoint surfaces as 502
// (not 401 — that would log the user out). The netguard client refuses the
// loopback target, so discovery errors.
func TestDiscover_UpstreamError(t *testing.T) {
	st, router := newDiscoverTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	body := `{"type":"ical","config":{"username":"u","password":"p","endpoint":"http://127.0.0.1:1"}}`
	if code := discoverReq(router, body, jwt); code != http.StatusBadGateway {
		t.Errorf("upstream error: expected 502, got %d", code)
	}
}
