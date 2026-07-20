package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newBodyLimitRouter mounts a POST /echo that decodes its body via
// decodeJSONBody behind a maxBytesMiddleware(limit), so tests can drive the
// exact request-size path the production router uses.
func newBodyLimitRouter(limit int64) http.Handler {
	r := chi.NewRouter()
	r.Use(maxBytesMiddleware(limit))
	r.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if !decodeJSONBody(w, req, &body) {
			return
		}
		writeJSON(w, http.StatusOK, body)
	})
	return r
}

func postBody(router http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestMaxBytesMiddleware_RejectsOversizeBody(t *testing.T) {
	router := newBodyLimitRouter(64)

	// A body past the 64-byte cap surfaces as 413 (not 400).
	oversize := `{"v":"` + strings.Repeat("a", 128) + `"}`
	w := postBody(router, oversize)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: expected 413, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Errorf("expected 'request body too large' envelope, got %s", w.Body.String())
	}

	// A small valid body still decodes and returns 200.
	w = postBody(router, `{"v":"ok"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("small body: expected 200, got %d", w.Code)
	}
}

func TestDecodeJSONBody_Malformed(t *testing.T) {
	router := newBodyLimitRouter(maxRequestBodyBytes)

	w := postBody(router, `{not valid json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), errInvalidRequestBody) {
		t.Errorf("expected %q envelope, got %s", errInvalidRequestBody, w.Body.String())
	}
}
