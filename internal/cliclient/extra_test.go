package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryAllParams(t *testing.T) {
	q := SearchParams{
		Query:       "x",
		Limit:       3,
		Offset:      10,
		Sources:     []string{"imap"},
		SourceNames: []string{"work"},
		DateFrom:    "2026-01-01",
		DateTo:      "2026-02-01",
	}.query()

	for k, want := range map[string]string{
		"q": "x", "limit": "3", "offset": "10",
		"sources": "imap", "source_names": "work",
		"date_from": "2026-01-01", "date_to": "2026-02-01",
	} {
		if got := q.Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Has("score_details") {
		t.Fatal("score_details must be absent when Explain is false")
	}
}

func TestDoTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connections are now refused

	if _, err := New(url, "").Me(context.Background()); err == nil {
		t.Fatal("expected a transport error against a closed server")
	}
}

func TestDoBuildRequestError(t *testing.T) {
	// A control character in the URL makes http.NewRequestWithContext fail.
	if _, err := New("http://exa\x7fmple", "").Me(context.Background()); err == nil {
		t.Fatal("expected a build-request error for a malformed URL")
	}
}

func TestDecodeEnvelopeDataTypeMismatch(t *testing.T) {
	var out struct {
		N int `json:"n"`
	}
	// data is a string but out is a struct → the inner unmarshal fails.
	if err := decodeEnvelope(200, []byte(`{"data":"not an object"}`), &out); err == nil {
		t.Fatal("expected a data decode error")
	}
}

func TestMeAndCreateTokenErrorPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	ctx := context.Background()

	if _, err := New(srv.URL, "t").Me(ctx); err == nil {
		t.Fatal("expected Me error on 500")
	}
	if _, _, err := New(srv.URL, "t").CreateToken(ctx, "n"); err == nil {
		t.Fatal("expected CreateToken error on 500")
	}
}
