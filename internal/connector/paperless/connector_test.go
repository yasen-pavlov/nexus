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
		result, err := c.Fetch(context.Background(), nil)
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
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

		// Doc 2 has no correspondent or document_type
		doc2 := result.Documents[1]
		if _, ok := doc2.Metadata["correspondent"]; ok {
			t.Error("expected no correspondent on doc 2")
		}

		// Verify cursor
		if result.Cursor == nil {
			t.Fatal("expected cursor")
		}
		if result.Cursor.ItemsSynced != 2 {
			t.Errorf("expected 2 items synced, got %d", result.Cursor.ItemsSynced)
		}
	})

	t.Run("incremental sync with cursor", func(t *testing.T) {
		cursor := &model.SyncCursor{
			CursorData: map[string]any{
				"last_sync_time": now.Add(-1 * time.Hour).Format(time.RFC3339Nano),
			},
		}
		result, err := c.Fetch(context.Background(), cursor)
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		// Our mock doesn't actually filter, but verify the cursor was used
		if len(result.Documents) != 2 {
			t.Fatalf("expected 2 documents, got %d", len(result.Documents))
		}
	})
}

func TestFetch_Pagination(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()

	// Empty lookups
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}}) //nolint:errcheck // test
		})
	}

	srv := httptest.NewServer(mux)
	// Need to set up document handler with reference to srv.URL for next link
	page2URL := ""

	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
	result, err := c.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("expected 2 documents across 2 pages, got %d", len(result.Documents))
	}
	// 2 paginated content fetches + 1 enumerateAllIDs pass for the
	// deletion-sync source-id list.
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 page fetches + 1 enumerate), got %d", callCount)
	}
}

func TestFetch_PopulatesCurrentSourceIDs(t *testing.T) {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(lookupResponse{Results: []lookupItem{}})
		})
	}
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		// `fields=id` indicates the enumeration pass — return the
		// id-only shape. Anything else is the regular fetch path.
		if r.URL.Query().Get("fields") == "id" {
			_, _ = w.Write([]byte(`{"results":[{"id":1},{"id":2},{"id":3}],"next":null}`))
			return
		}
		_ = json.NewEncoder(w).Encode(paginatedResponse{
			Results: []paperlessDoc{{ID: 1, Title: "T", Added: time.Now(), Modified: time.Now()}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Connector{name: "test", baseURL: srv.URL, token: "k", client: srv.Client()}
	result, err := c.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "2", "3"}
	if !reflect.DeepEqual(result.CurrentSourceIDs, want) {
		t.Errorf("CurrentSourceIDs = %v, want %v", result.CurrentSourceIDs, want)
	}
}

func TestFetch_EnumerationFailureDoesNotAbortFetch(t *testing.T) {
	// A 500 on the id-only enumerate must NOT fail the sync — we still
	// want to index the docs returned by the regular fetch. The
	// enumeration error just opts the connector out of deletion sync
	// for this round (CurrentSourceIDs == nil).
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
	result, err := c.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatalf("Fetch should succeed even when enumeration fails, got %v", err)
	}
	if len(result.Documents) != 1 {
		t.Errorf("expected 1 indexed doc, got %d", len(result.Documents))
	}
	if result.CurrentSourceIDs != nil {
		t.Errorf("CurrentSourceIDs must be nil on enumeration failure, got %v", result.CurrentSourceIDs)
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

	_, err := c.Fetch(ctx, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestFetch_LookupHTTPError(t *testing.T) {
	// Tags endpoint returns 500 — Fetch should propagate the error.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	if _, err := c.Fetch(context.Background(), nil); err == nil {
		t.Fatal("expected error from 500 on tags lookup")
	}
}

func TestFetch_LookupBadJSON(t *testing.T) {
	// Tags endpoint returns malformed JSON — fetchLookup should error out.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json")) //nolint:errcheck // test
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	if _, err := c.Fetch(context.Background(), nil); err == nil {
		t.Fatal("expected error from malformed JSON")
	}
}

func TestFetch_DocumentsHTTPError(t *testing.T) {
	// Lookups succeed but documents endpoint returns 500.
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
	if _, err := c.Fetch(context.Background(), nil); err == nil {
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
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json")) //nolint:errcheck // test
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Connector{name: "test", baseURL: srv.URL, token: "test", client: srv.Client()}
	if _, err := c.Fetch(context.Background(), nil); err == nil {
		t.Fatal("expected error from malformed documents JSON")
	}
}

func TestFetch_LookupPagination(t *testing.T) {
	// Lookup page 1 has a Next pointer to page 2 — verify fetchLookup follows it.
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
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
	result, err := c.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
