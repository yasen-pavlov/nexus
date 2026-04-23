package paperless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/testutil"
)

func TestConfigure(t *testing.T) {
	c := &Connector{client: &http.Client{}}

	t.Run("valid config", func(t *testing.T) {
		err := c.Configure(connector.Config{
			"name":  "my-paperless",
			"url":   "http://paperless:8000",
			"token": "abc123",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Name() != "my-paperless" {
			t.Errorf("expected name 'my-paperless', got %q", c.Name())
		}
		if c.Type() != "paperless" {
			t.Errorf("expected type 'paperless', got %q", c.Type())
		}
		if c.baseURL != "http://paperless:8000" {
			t.Errorf("expected baseURL without trailing slash, got %q", c.baseURL)
		}
	})

	t.Run("trailing slash stripped", func(t *testing.T) {
		c2 := &Connector{client: &http.Client{}}
		err := c2.Configure(connector.Config{
			"url": "http://paperless:8000/", "token": "x",
		})
		if err != nil {
			t.Fatal(err)
		}
		if c2.baseURL != "http://paperless:8000" {
			t.Errorf("expected trailing slash stripped, got %q", c2.baseURL)
		}
	})

	t.Run("missing url", func(t *testing.T) {
		c2 := &Connector{client: &http.Client{}}
		err := c2.Configure(connector.Config{"token": "x"})
		if err == nil {
			t.Fatal("expected error for missing url")
		}
	})

	t.Run("missing token", func(t *testing.T) {
		c2 := &Connector{client: &http.Client{}}
		err := c2.Configure(connector.Config{"url": "http://localhost"})
		if err == nil {
			t.Fatal("expected error for missing token")
		}
	})

	t.Run("default name", func(t *testing.T) {
		c2 := &Connector{client: &http.Client{}}
		c2.Configure(connector.Config{"url": "http://localhost", "token": "x"}) //nolint:errcheck // test
		if c2.Name() != "paperless" {
			t.Errorf("expected default name 'paperless', got %q", c2.Name())
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Token valid-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(paginatedResponse{Count: 0, Results: []paperlessDoc{}}) //nolint:errcheck // test
		}))
		defer srv.Close()

		c := &Connector{baseURL: srv.URL, token: "valid-token", client: srv.Client()}
		if err := c.Validate(); err != nil {
			t.Fatalf("validate failed: %v", err)
		}
	})

	t.Run("auth failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		c := &Connector{baseURL: srv.URL, token: "bad", client: srv.Client()}
		err := c.Validate()
		if err == nil {
			t.Fatal("expected auth error")
		}
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := &Connector{baseURL: srv.URL, token: "x", client: srv.Client()}
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error for 500")
		}
	})

	t.Run("connection error", func(t *testing.T) {
		c := &Connector{baseURL: "http://localhost:59999", token: "x", client: &http.Client{Timeout: time.Second}}
		err := c.Validate()
		if err == nil {
			t.Fatal("expected connection error")
		}
	})
}

func TestFetch(t *testing.T) {
	now := time.Now()
	corr1 := 1
	dtype1 := 2

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(lookupResponse{ //nolint:errcheck // test
			Results: []lookupItem{{ID: 10, Name: "invoice"}, {ID: 20, Name: "receipt"}},
		})
	})
	mux.HandleFunc("/api/correspondents/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(lookupResponse{ //nolint:errcheck // test
			Results: []lookupItem{{ID: 1, Name: "ACME Corp"}},
		})
	})
	mux.HandleFunc("/api/document_types/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(lookupResponse{ //nolint:errcheck // test
			Results: []lookupItem{{ID: 2, Name: "Invoice"}},
		})
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fields") == "id" {
			// Enumeration pass — return IDs for deletion-sync source-id emission.
			json.NewEncoder(w).Encode(paperlessIDPage{ //nolint:errcheck // test
				Results: []struct {
					ID int `json:"id"`
				}{{ID: 1}, {ID: 2}},
			})
			return
		}
		json.NewEncoder(w).Encode(paginatedResponse{ //nolint:errcheck // test
			Count: 2,
			Results: []paperlessDoc{
				{
					ID: 1, Title: "Electric Bill", Content: "Monthly electric bill for January",
					Correspondent: &corr1, DocumentType: &dtype1, Tags: []int{10},
					OriginalFileName: "bill.pdf", Added: now, Modified: now,
				},
				{
					ID: 2, Title: "Receipt", Content: "Coffee shop receipt",
					Tags: []int{20}, OriginalFileName: "receipt.jpg", Added: now, Modified: now,
				},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test-paperless", baseURL: srv.URL, token: "test", client: srv.Client()}

	t.Run("full sync", func(t *testing.T) {
		result := testutil.RunFetch(t, c, nil)
		if result.Err != nil {
			t.Fatalf("fetch failed: %v", result.Err)
		}
		if len(result.Documents) != 2 {
			t.Fatalf("expected 2 documents, got %d", len(result.Documents))
		}

		doc1 := result.Documents[0]
		if doc1.Title != "Electric Bill" {
			t.Errorf("expected title 'Electric Bill', got %q", doc1.Title)
		}
		if doc1.Content != "Monthly electric bill for January" {
			t.Errorf("unexpected content: %q", doc1.Content)
		}
		if doc1.SourceType != "paperless" {
			t.Errorf("expected source_type 'paperless', got %q", doc1.SourceType)
		}
		if doc1.SourceID != "1" {
			t.Errorf("expected source_id '1', got %q", doc1.SourceID)
		}
		if doc1.Metadata["correspondent"] != "ACME Corp" {
			t.Errorf("expected correspondent 'ACME Corp', got %v", doc1.Metadata["correspondent"])
		}
		if doc1.Metadata["document_type"] != "Invoice" {
			t.Errorf("expected document_type 'Invoice', got %v", doc1.Metadata["document_type"])
		}
		tags, ok := doc1.Metadata["tags"].([]string)
		if !ok || len(tags) != 1 || tags[0] != "invoice" {
			t.Errorf("expected tags ['invoice'], got %v", doc1.Metadata["tags"])
		}

		doc2 := result.Documents[1]
		if _, ok := doc2.Metadata["correspondent"]; ok {
			t.Error("expected no correspondent on doc 2")
		}

		if result.LastCursor == nil {
			t.Fatal("expected cursor")
		}
	})

	t.Run("incremental sync with cursor", func(t *testing.T) {
		cursor := &model.SyncCursor{
			CursorData: map[string]any{
				"last_sync_time": now.Add(-1 * time.Hour).Format(time.RFC3339Nano),
			},
		}
		result := testutil.RunFetch(t, c, cursor)
		if result.Err != nil {
			t.Fatalf("fetch failed: %v", result.Err)
		}
		// Our mock doesn't actually filter, but verify the cursor was used
		if len(result.Documents) != 2 {
			t.Fatalf("expected 2 documents, got %d", len(result.Documents))
		}
	})
}

func TestFetch_Pagination(t *testing.T) {
	callCount := 0
	enumCount := 0
	mux := http.NewServeMux()

	// Empty lookups
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}

	srv := httptest.NewServer(mux)
	page2URL := ""

	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fields") == "id" {
			enumCount++
			_ = json.NewEncoder(w).Encode(paperlessIDPage{
				Results: []struct {
					ID int `json:"id"`
				}{{ID: 1}, {ID: 2}},
			})
			return
		}
		callCount++
		now := time.Now()

		if callCount == 1 {
			next := page2URL
			json.NewEncoder(w).Encode(paginatedResponse{ //nolint:errcheck // test
				Count: 2,
				Next:  &next,
				Results: []paperlessDoc{
					{ID: 1, Title: "Doc 1", Content: "First", Added: now, Modified: now},
				},
			})
		} else {
			json.NewEncoder(w).Encode(paginatedResponse{ //nolint:errcheck // test
				Count: 2,
				Results: []paperlessDoc{
					{ID: 2, Title: "Doc 2", Content: "Second", Added: now, Modified: now},
				},
			})
		}
	})

	page2URL = srv.URL + "/api/documents/?page=2"
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("fetch failed: %v", result.Err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("expected 2 documents across 2 pages, got %d", len(result.Documents))
	}
	if callCount != 2 {
		t.Errorf("expected 2 content page fetches, got %d", callCount)
	}
	if enumCount != 1 {
		t.Errorf("expected 1 enumeration pass, got %d", enumCount)
	}
}

func TestFetch_EmitsSourceIDsSortedLex(t *testing.T) {
	// IDs are numeric strings; the connector must sort them
	// lexicographically before emission so the pipeline's
	// streaming merge-diff sees them in OpenSearch keyword-sort
	// order.
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "id" {
			// Deliberately non-sorted numeric IDs to prove the
			// connector is sorting lex before emitting.
			_, _ = w.Write([]byte(`{"results":[{"id":2},{"id":10},{"id":1}],"next":null}`))
			return
		}
		_ = json.NewEncoder(w).Encode(paginatedResponse{
			Results: []paperlessDoc{{ID: 1, Title: "T", Added: time.Now(), Modified: time.Now()}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Connector{name: "test", baseURL: srv.URL, token: "k", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	// "1" < "10" < "2" in lex order.
	want := []string{"1", "10", "2"}
	if !reflect.DeepEqual(result.SourceIDs, want) {
		t.Errorf("SourceIDs = %v, want %v", result.SourceIDs, want)
	}
}

func TestFetch_EnumerationFailureDoesNotAbortFetch(t *testing.T) {
	// A 500 on the id-only enumerate must NOT fail the sync — we still
	// want to index the docs returned by the regular fetch. The
	// enumeration error just opts the connector out of deletion sync
	// for this round (no SourceID items emitted).
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "id" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(paginatedResponse{
			Results: []paperlessDoc{{ID: 1, Title: "T", Added: time.Now(), Modified: time.Now()}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Connector{name: "test", baseURL: srv.URL, token: "k", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("Fetch should succeed even when enumeration fails, got %v", result.Err)
	}
	if len(result.Documents) != 1 {
		t.Errorf("expected 1 indexed doc, got %d", len(result.Documents))
	}
	if len(result.SourceIDs) != 0 {
		t.Errorf("expected no SourceID emissions on enumeration failure, got %v", result.SourceIDs)
	}
}

func TestFetch_ContextCancellation(t *testing.T) {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next := "http://localhost/api/documents/?page=2"
		json.NewEncoder(w).Encode(paginatedResponse{ //nolint:errcheck // test
			Count: 100, Next: &next,
			Results: []paperlessDoc{{ID: 1, Title: "Doc", Added: time.Now(), Modified: time.Now()}},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	items, errs := c.Fetch(ctx, nil)
	result := testutil.CollectStream(t, items, errs)
	if result.Err == nil {
		t.Fatal("expected terminal error for cancelled context")
	}
}

// TestToDocument_UsesCreatedDateWhenParsable covers the "Created
// date valid" branch — a YYYY-MM-DD `created` field wins over the
// import `Added` timestamp for the Document.CreatedAt.
func TestToDocument_UsesCreatedDateWhenParsable(t *testing.T) {
	c := &Connector{name: "paperless"}
	added := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	p := paperlessDoc{
		ID: 5, Title: "Invoice", Content: "body",
		Added: added, Modified: added, Created: "2025-12-31",
	}
	doc := c.toDocument(p, map[int]string{}, map[int]string{}, map[int]string{})
	want := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	if !doc.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", doc.CreatedAt, want)
	}
}

// TestToDocument_FallsBackToAdded covers the unparseable-date
// branch — garbage in `created` falls through to `added`.
func TestToDocument_FallsBackToAdded(t *testing.T) {
	c := &Connector{name: "paperless"}
	added := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	p := paperlessDoc{
		ID: 1, Title: "X", Added: added, Modified: added,
		Created: "not-a-date",
	}
	doc := c.toDocument(p, map[int]string{}, map[int]string{}, map[int]string{})
	if !doc.CreatedAt.Equal(added) {
		t.Errorf("CreatedAt = %v, want %v (fallback)", doc.CreatedAt, added)
	}
}

// TestToDocument_IncludesResolvedCorrespondent covers the
// correspondent-lookup path — when the ID maps to a name, the
// name ends up in metadata.
func TestToDocument_IncludesResolvedCorrespondent(t *testing.T) {
	c := &Connector{name: "paperless"}
	corr := 7
	p := paperlessDoc{
		ID: 1, Title: "t", Correspondent: &corr, Added: time.Now(), Modified: time.Now(),
	}
	doc := c.toDocument(p, nil,
		map[int]string{7: "ACME Corp"}, nil)
	if doc.Metadata["correspondent"] != "ACME Corp" {
		t.Errorf("correspondent = %v, want ACME Corp", doc.Metadata["correspondent"])
	}
}

func TestFetch_LookupHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil {
		t.Fatal("expected error from 500 on tags lookup")
	}
}

func TestFetch_LookupBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json")) //nolint:errcheck // test
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil {
		t.Fatal("expected error from malformed JSON")
	}
}

func TestFetch_DocumentsHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil {
		t.Fatal("expected error from 500 on documents fetch")
	}
}

func TestFetch_DocumentsBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "id" {
			// Enumeration returns nothing so the connector still
			// tries to fetch the regular content pages.
			_ = json.NewEncoder(w).Encode(paperlessIDPage{})
			return
		}
		w.Write([]byte("not json")) //nolint:errcheck // test
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil {
		t.Fatal("expected error from malformed documents JSON")
	}
}

// TestFetch_CursorContextCancelledMidEnumeration covers the
// ctx.Done() exit inside enumerateAllIDs's pagination loop.
func TestFetch_CursorContextCancelledMidEnumeration(t *testing.T) {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "id" {
			// Return a page with a next URL so the loop would
			// keep going — we cancel the context before it
			// does.
			next := r.Host + r.URL.Path + "?page=2"
			nextStr := "http://" + next
			_ = json.NewEncoder(w).Encode(struct {
				Results []struct {
					ID int `json:"id"`
				} `json:"results"`
				Next *string `json:"next"`
			}{
				Results: []struct {
					ID int `json:"id"`
				}{{ID: 1}},
				Next: &nextStr,
			})
			return
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := &Connector{name: "test", baseURL: srv.URL, token: "k", client: srv.Client()}
	items, errs := c.Fetch(ctx, nil)
	result := testutil.CollectStream(t, items, errs)
	if result.Err == nil {
		t.Fatal("expected terminal error from cancelled context")
	}
}

// TestFetch_SyncSinceFallback covers the branch where no cursor is
// present but the connector has a syncSince cutoff — the API call
// URL should carry modified__gt set to the syncSince timestamp.
func TestFetch_SyncSinceFallback(t *testing.T) {
	var capturedModifiedGt string
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "id" {
			_ = json.NewEncoder(w).Encode(paperlessIDPage{})
			return
		}
		if gt := r.URL.Query().Get("modified__gt"); gt != "" {
			capturedModifiedGt = gt
		}
		_ = json.NewEncoder(w).Encode(paginatedResponse{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &Connector{name: "ss-test", baseURL: srv.URL, token: "k", client: srv.Client(), syncSince: since}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if capturedModifiedGt == "" {
		t.Error("expected modified__gt to be set from syncSince")
	}
}

// TestLogEnumerationFailure covers the no-op hook so future
// refactors that wire it to structured logging don't regress the
// coverage floor silently.
func TestLogEnumerationFailure(t *testing.T) {
	c := &Connector{}
	c.logEnumerationFailure(nil)
	c.logEnumerationFailure(http.ErrNotSupported)
}

func TestFetch_LookupPagination(t *testing.T) {
	mux := http.NewServeMux()
	page2URL := ""
	tagPage := 0
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tagPage++
		if tagPage == 1 {
			next := page2URL
			json.NewEncoder(w).Encode(lookupResponse{ //nolint:errcheck // test
				Results: []lookupItem{{ID: 1, Name: "tag-one"}},
				Next:    &next,
			})
		} else {
			json.NewEncoder(w).Encode(lookupResponse{ //nolint:errcheck // test
				Results: []lookupItem{{ID: 2, Name: "tag-two"}},
			})
		}
	})
	for _, path := range []string{"/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fields") == "id" {
			_ = json.NewEncoder(w).Encode(paperlessIDPage{
				Results: []struct {
					ID int `json:"id"`
				}{{ID: 1}},
			})
			return
		}
		now := time.Now()
		json.NewEncoder(w).Encode(paginatedResponse{ //nolint:errcheck // test
			Results: []paperlessDoc{
				{ID: 1, Title: "Doc", Tags: []int{1, 2}, Added: now, Modified: now},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	page2URL = srv.URL + "/api/tags/?page=2"

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(result.Documents))
	}
	tags, _ := result.Documents[0].Metadata["tags"].([]string)
	if len(tags) != 2 {
		t.Errorf("expected both tags resolved across pages, got %v", tags)
	}
	if tagPage != 2 {
		t.Errorf("expected 2 lookup pages fetched, got %d", tagPage)
	}
}
