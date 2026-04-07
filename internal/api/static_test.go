package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticFileHandler(t *testing.T) {
	handler := staticFileHandler()
	if handler == nil {
		// In test environment without built assets, the .gitkeep is the only file
		// This is expected — the handler returns nil when there's only .gitkeep
		// or returns a handler if static files exist
		t.Log("static handler returned nil (no built frontend assets), which is expected in dev/test")
		return
	}

	// If we have a handler, test that it serves files
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /, got %d", w.Code)
	}
}
