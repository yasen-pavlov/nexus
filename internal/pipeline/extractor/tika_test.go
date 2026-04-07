package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTika_CanExtract(t *testing.T) {
	tika := NewTika("http://localhost:9998")

	tests := []struct {
		contentType string
		want        bool
	}{
		{"application/pdf", true},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", true},
		{"image/png", true},
		{"text/plain", false},
		{"text/markdown", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := tika.CanExtract(tt.contentType); got != tt.want {
			t.Errorf("CanExtract(%q) = %v, want %v", tt.contentType, got, tt.want)
		}
	}
}

func TestTika_Extract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("  Extracted text from PDF  \n")) //nolint:errcheck // test
	}))
	defer srv.Close()

	tika := NewTika(srv.URL)
	text, err := tika.Extract(context.Background(), []byte("fake pdf bytes"))
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if text != "Extracted text from PDF" {
		t.Errorf("expected trimmed text, got %q", text)
	}
}

func TestTika_Extract_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tika := NewTika(srv.URL)
	_, err := tika.Extract(context.Background(), []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTika_Extract_ConnectionError(t *testing.T) {
	tika := NewTika("http://localhost:59999")
	_, err := tika.Extract(context.Background(), []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTika_Available(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tika := NewTika(srv.URL)
	if !tika.Available(context.Background()) {
		t.Error("expected available")
	}
}

func TestTika_NotAvailable(t *testing.T) {
	tika := NewTika("http://localhost:59999")
	if tika.Available(context.Background()) {
		t.Error("expected not available")
	}
}

func TestRegistry_WithTika(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.Write([]byte("extracted")) //nolint:errcheck // test
			return
		}
		w.WriteHeader(http.StatusOK) // health check
	}))
	defer srv.Close()

	r := NewRegistry(srv.URL)
	if !r.CanExtract("application/pdf") {
		t.Error("expected PDF extractable with Tika")
	}
	text, err := r.Extract(context.Background(), "application/pdf", []byte("fake"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "extracted" {
		t.Errorf("expected 'extracted', got %q", text)
	}
}

func TestRegistry_PlainText(t *testing.T) {
	r := NewRegistry("")
	text, err := r.Extract(context.Background(), "text/plain", []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Errorf("expected 'hello world', got %q", text)
	}
}

func TestRegistry_CanExtract(t *testing.T) {
	r := NewRegistry("")
	if !r.CanExtract("text/plain") {
		t.Error("expected plain text to be extractable")
	}
	if r.CanExtract("application/pdf") {
		t.Error("expected PDF not extractable without Tika")
	}
}

func TestRegistry_NoExtractor(t *testing.T) {
	r := NewRegistry("")
	_, err := r.Extract(context.Background(), "application/pdf", []byte("data"))
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}
