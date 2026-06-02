//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// ragRouter wires up just the /api/settings/rag endpoints with the same
// auth middleware stack the production router uses, so auth-boundary tests
// fire the real chain (anon -> 401, regular user -> 403, admin -> 200).
func ragRouter(t *testing.T) (*store.Store, *RAGManager, http.Handler, string) {
	t.Helper()
	st, _, _ := newTestDeps(t)

	mgr := NewRAGManager(st, zap.NewNop())
	if err := mgr.LoadFromDB(context.Background(), nil); err != nil {
		t.Fatalf("load rag settings: %v", err)
	}

	h := &handler{store: st, ragMgr: mgr, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Use(auth.Middleware(testJWTSecret))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin"))
		r.Get("/api/settings/rag", h.GetRAGSettings)
		r.Put("/api/settings/rag", h.UpdateRAGSettings)
	})

	_, token := createTestAdmin(t, st)
	return st, mgr, r, token
}

func TestGetRAGSettings_AdminOK_DefaultMaxToolRounds(t *testing.T) {
	_, _, router, token := ragRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/rag", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if got := data["max_tool_rounds"]; got != float64(defaultMaxToolRounds) {
		t.Errorf("max_tool_rounds = %v, want default %d", got, defaultMaxToolRounds)
	}
}

func TestGetRAGSettings_AnonRejected(t *testing.T) {
	_, _, router, _ := ragRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/rag", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestGetRAGSettings_NonAdminRejected(t *testing.T) {
	st, _, router, _ := ragRouter(t)
	_, userToken := createTestUser(t, st)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/rag", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestUpdateRAGSettings_PersistsAndHotSwaps(t *testing.T) {
	st, mgr, router, token := ragRouter(t)

	body := `{"max_tool_rounds":4}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Snapshot is hot-swapped — manager reflects the new value immediately.
	if got := mgr.MaxToolRounds(); got != 4 {
		t.Errorf("MaxToolRounds = %d, want 4", got)
	}

	// DB persists the value.
	stored, err := st.GetSetting(context.Background(), "rag_max_tool_rounds")
	if err != nil {
		t.Fatalf("get stored: %v", err)
	}
	if stored != "4" {
		t.Errorf("stored value = %q, want %q", stored, "4")
	}
}

func TestUpdateRAGSettings_AnonRejected(t *testing.T) {
	_, _, router, _ := ragRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString(`{"max_tool_rounds":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestUpdateRAGSettings_NonAdminRejected(t *testing.T) {
	st, _, router, _ := ragRouter(t)
	_, userToken := createTestUser(t, st)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString(`{"max_tool_rounds":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestUpdateRAGSettings_BadBody(t *testing.T) {
	_, _, router, token := ragRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateRAGSettings_OutOfRangeRejected(t *testing.T) {
	_, _, router, token := ragRouter(t)

	for _, body := range []string{
		`{"max_tool_rounds":-1}`,
		`{"max_tool_rounds":99}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: expected 400, got %d", body, w.Code)
		}
	}
}

// MaxToolRounds = 0 is the documented "tools disabled" sentinel and must
// be accepted (not collapsed to the default by the validator).
func TestUpdateRAGSettings_ZeroAccepted(t *testing.T) {
	_, mgr, router, token := ragRouter(t)

	body := `{"max_tool_rounds":0}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rag", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if got := mgr.MaxToolRounds(); got != 0 {
		t.Errorf("MaxToolRounds = %d, want 0", got)
	}
}
