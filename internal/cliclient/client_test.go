package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/model"
)

// writeData wraps data in the server's success envelope.
func writeData(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// asAPIError asserts err is an *APIError with the given status + message.
func asAPIError(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != status {
		t.Fatalf("status = %d, want %d", apiErr.StatusCode, status)
	}
	if msg != "" && apiErr.Message != msg {
		t.Fatalf("message = %q, want %q", apiErr.Message, msg)
	}
}

func TestDecodeEnvelopeSuccess(t *testing.T) {
	var got struct {
		Name string `json:"name"`
	}
	if err := decodeEnvelope(200, []byte(`{"data":{"name":"x"}}`), &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "x" {
		t.Fatalf("got %q, want x", got.Name)
	}

	// An empty body (204) has nothing to decode and is not an error.
	if err := decodeEnvelope(204, []byte("  \n"), nil); err != nil {
		t.Fatalf("204: unexpected error: %v", err)
	}

	// A 2xx with a non-JSON body is a real decode error.
	if err := decodeEnvelope(200, []byte("not json"), nil); err == nil {
		t.Fatal("want decode error for non-JSON success body, got nil")
	}
}

func TestDecodeEnvelopeErrors(t *testing.T) {
	asAPIError(t, decodeEnvelope(400, []byte(`{"error":"bad request"}`), nil), 400, "bad request")
	asAPIError(t, decodeEnvelope(502, []byte("upstream down"), nil), 502, "upstream down")
}

func TestAPIErrorMessage(t *testing.T) {
	withMsg := (&APIError{StatusCode: 404, Message: "not found"}).Error()
	if withMsg != "not found (status 404)" {
		t.Fatalf("got %q", withMsg)
	}
	noMsg := (&APIError{StatusCode: 500}).Error()
	if noMsg != "server returned 500 Internal Server Error" {
		t.Fatalf("got %q", noMsg)
	}
}

func TestSearchSendsAuthAndParams(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		writeData(w, 200, model.SearchResult{
			Query:      "hello",
			TotalCount: 1,
			Documents:  []model.DocumentHit{{Document: model.Document{Title: "Doc A"}}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "nexus_pat_abc") // trailing slash must be trimmed
	res, err := c.Search(context.Background(), SearchParams{
		Query:   "hello",
		Limit:   5,
		Sources: []string{"imap", "telegram"},
		Explain: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotAuth != "Bearer nexus_pat_abc" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	for _, want := range []string{"q=hello", "limit=5", "sources=imap%2Ctelegram", "score_details=true"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
	if len(res.Documents) != 1 || res.Documents[0].Title != "Doc A" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestSearchPropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "authentication required"})
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Search(context.Background(), SearchParams{Query: "x"})
	asAPIError(t, err, 401, "authentication required")
}

func TestNoTokenOmitsAuthHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		writeData(w, 200, model.SearchResult{})
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "").Search(context.Background(), SearchParams{Query: "x"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if hadAuth {
		t.Fatal("expected no Authorization header when token is empty")
	}
}

func TestMeLoginCreateToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, 200, User{Username: "muty", Role: "admin"})
	})
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "muty" || body["password"] != "secret" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid username or password"})
			return
		}
		writeData(w, 200, map[string]any{"token": "jwt-123"})
	})
	mux.HandleFunc("/api/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-123" {
			w.WriteHeader(403)
			return
		}
		writeData(w, 201, map[string]any{
			"token": "nexus_pat_minted",
			"meta":  model.APIToken{Name: "nexus-cli"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	user, err := New(srv.URL, "tok").Me(ctx)
	if err != nil || user.Username != "muty" {
		t.Fatalf("me: %v / %+v", err, user)
	}

	jwt, err := New(srv.URL, "").Login(ctx, "muty", "secret")
	if err != nil || jwt != "jwt-123" {
		t.Fatalf("login: %v / %q", err, jwt)
	}

	pat, meta, err := New(srv.URL, jwt).CreateToken(ctx, "nexus-cli")
	if err != nil || pat != "nexus_pat_minted" || meta == nil {
		t.Fatalf("create token: %v / %q / %+v", err, pat, meta)
	}

	if _, err := New(srv.URL, "").Login(ctx, "muty", "wrong"); err == nil {
		t.Fatal("expected login failure with wrong password")
	}
}
