package paperless

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/muty/nexus/internal/testutil"
)

// TestFetch_MidRunCheckpointUsesLastDocModified pins that a mid-run checkpoint
// carries the last emitted doc's Modified time (a safe resume point for the
// modified-asc stream), NOT the run-start timestamp — otherwise an interrupted
// large sync would stamp run-start and permanently skip every unfetched page.
func TestFetch_MidRunCheckpointUsesLastDocModified(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	docModified := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	const total = 201 // one page-of-100 boundary past the 200-doc checkpoint

	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fields") == "id" {
			_ = json.NewEncoder(w).Encode(paperlessIDPage{Results: []struct {
				ID int `json:"id"`
			}{{ID: 1}}})
			return
		}
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		start := (page - 1) * 100
		end := min(start+100, total)
		results := make([]paperlessDoc, 0, end-start)
		for i := start; i < end; i++ {
			results = append(results, paperlessDoc{
				ID: i + 1, Title: "Doc", Content: "x",
				Added: docModified(i), Modified: docModified(i),
			})
		}
		var next *string
		if end < total {
			n := srv.URL + "/api/documents/?page=" + strconv.Itoa(page+1)
			next = &n
		}
		_ = json.NewEncoder(w).Encode(paginatedResponse{Count: total, Next: next, Results: results})
	})

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Checkpoints) < 2 {
		t.Fatalf("expected a mid-run + final checkpoint, got %d", len(result.Checkpoints))
	}

	// The mid-run checkpoint (emitted after doc 200) must carry doc #200's
	// Modified time (index 199), not the run-start wall clock.
	want := docModified(199).Format(time.RFC3339Nano)
	if got, _ := result.Checkpoints[0].CursorData["last_sync_time"].(string); got != want {
		t.Errorf("mid-run checkpoint last_sync_time = %q, want doc#200 Modified %q", got, want)
	}

	// The final checkpoint stamps run-start (~now), well after the 2025 seed.
	final := result.Checkpoints[len(result.Checkpoints)-1]
	got, _ := final.CursorData["last_sync_time"].(string)
	ft, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("final checkpoint not parseable: %q (%v)", got, err)
	}
	if ft.Year() == 2025 {
		t.Errorf("final checkpoint should be run-start (now), got seeded time %q", got)
	}
}
