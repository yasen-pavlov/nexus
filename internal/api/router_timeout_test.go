package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// TestTimeoutMiddlewareScope locks in the routing invariant the SSE-timeout fix
// relies on: chimw.Timeout registered inside the /api sub-router must NOT reach
// sibling groups mounted directly on the root mux, where the long-lived SSE
// streams (sync progress, chat message stream) live. NewRouter deliberately
// moved Timeout off the root mux and into the /api group for exactly this
// reason — if it ever moves back to the root, the streams are force-closed at
// 10 minutes again. This mirrors NewRouter's topology with a short timeout so
// the behavior is observable in a unit test.
func TestTimeoutMiddlewareScope(t *testing.T) {
	const timeout = 40 * time.Millisecond

	// A handler that would run well past the timeout unless its context is
	// canceled. When chi's Timeout fires it cancels the context AND writes 504,
	// so we just return on cancellation and let Timeout own the response.
	slow := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(8 * timeout):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	}

	r := chi.NewRouter()
	// Sibling SSE-style group on the ROOT mux — no Timeout, like the real
	// /api/sync/progress and /api/chats/{id}/messages streams.
	r.Group(func(r chi.Router) {
		r.Get("/api/sync/progress", slow)
	})
	// /api sub-router carries the Timeout, like the real regular API routes.
	r.Route("/api", func(r chi.Router) {
		r.Use(chimw.Timeout(timeout))
		r.Get("/health", slow)
	})

	// A regular /api route is cut at the timeout -> 504.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("/api/health should be timed out (504), got %d", rec.Code)
	}

	// The SSE route on the root mux outlasts the timeout -> 200 (not force-closed).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sync/progress", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/sync/progress must escape the /api timeout, got %d", rec.Code)
	}
}
