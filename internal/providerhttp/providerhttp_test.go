package providerhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type echoReq struct {
	Name string `json:"name"`
}

type echoResp struct {
	Greeting string `json:"greeting"`
}

// errProviderRejected is what onHTTPError returns so tests can assert the callback's
// error is propagated verbatim (via errors.Is) rather than wrapped away.
var errProviderRejected = errors.New("provider rejected request")

func TestPostJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key-123" {
			t.Errorf("Authorization = %q, want Bearer key-123", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		_, _ = w.Write([]byte(`{"greeting":"hi"}`))
	}))
	defer srv.Close()

	var out echoResp
	err := PostJSON(context.Background(), srv.Client(), srv.URL, "key-123", echoReq{Name: "x"}, &out,
		func(*http.Response) error { return errProviderRejected })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Greeting != "hi" {
		t.Errorf("greeting = %q, want hi", out.Greeting)
	}
}

func TestPostJSON_HTTPError_PropagatesCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var out echoResp
	err := PostJSON(context.Background(), srv.Client(), srv.URL, "k", echoReq{}, &out,
		func(resp *http.Response) error {
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("callback saw status %d, want 429", resp.StatusCode)
			}
			return errProviderRejected
		})
	if !errors.Is(err, errProviderRejected) {
		t.Errorf("expected the onHTTPError result verbatim, got %v", err)
	}
}

func TestPostJSON_BadJSON_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	var out echoResp
	err := PostJSON(context.Background(), srv.Client(), srv.URL, "k", echoReq{}, &out,
		func(*http.Response) error { return errProviderRejected })
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if errors.Is(err, errProviderRejected) {
		t.Error("a 200 with bad JSON must not route through onHTTPError")
	}
}

func TestPostJSON_MarshalError(t *testing.T) {
	// A channel can't be marshaled to JSON, so the marshal step fails before
	// any request is built.
	var out echoResp
	err := PostJSON(context.Background(), http.DefaultClient, "http://example.invalid", "k",
		make(chan int), &out, func(*http.Response) error { return errProviderRejected })
	if err == nil {
		t.Fatal("expected a marshal error")
	}
}

func TestPostJSON_CreateRequestError(t *testing.T) {
	// A control character in the URL trips http.NewRequestWithContext.
	var out echoResp
	err := PostJSON(context.Background(), http.DefaultClient, "http://invalid\x00host", "k",
		echoReq{}, &out, func(*http.Response) error { return errProviderRejected })
	if err == nil {
		t.Fatal("expected a request-construction error")
	}
}

func TestPostJSON_TransportError(t *testing.T) {
	// Port 1 refuses connections, forcing client.Do to fail.
	var out echoResp
	err := PostJSON(context.Background(), &http.Client{}, "http://127.0.0.1:1", "k",
		echoReq{}, &out, func(*http.Response) error { return errProviderRejected })
	if err == nil {
		t.Fatal("expected a transport error")
	}
}
