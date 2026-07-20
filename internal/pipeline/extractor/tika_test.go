package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/lang"
)

func TestRegistry_RegistersTikaEvenWhenUnreachable(t *testing.T) {
	// Boot-race fix: Tika must be registered whenever the URL is set, even if
	// it is unreachable at construction — otherwise a transient outage at boot
	// permanently disables binary extraction for the whole process lifetime.
	r := NewRegistry("http://127.0.0.1:59999", nil) // no listener
	if !r.CanExtract("application/pdf") {
		t.Fatal("Tika should be registered despite being unreachable")
	}
	_, err := r.Extract(context.Background(), "application/pdf", []byte("data"))
	if err == nil {
		t.Fatal("expected a per-request error from the unreachable Tika")
	}
	if strings.Contains(err.Error(), "no extractor available") {
		t.Errorf("Tika was not registered (got %q); a down Tika must surface a request error, not a missing extractor", err)
	}
}

func TestTika_CanExtract(t *testing.T) {
	tika := NewTika("http://localhost:9998", nil)

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
	var gotOCRLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotOCRLang = r.Header.Get("X-Tika-OCRLanguage")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("  Extracted text from PDF  \n")) //nolint:errcheck // test
	}))
	defer srv.Close()

	tika := NewTika(srv.URL, lang.Default())
	text, err := tika.Extract(context.Background(), []byte("fake pdf bytes"))
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if text != "Extracted text from PDF" {
		t.Errorf("expected trimmed text, got %q", text)
	}
	if gotOCRLang != "eng+deu+bul" {
		t.Errorf("X-Tika-OCRLanguage = %q, want %q", gotOCRLang, "eng+deu+bul")
	}
}

func TestTika_Extract_NoOCRLanguageWhenEmpty(t *testing.T) {
	var gotOCRLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOCRLang = r.Header.Get("X-Tika-OCRLanguage")
		w.Write([]byte("ok")) //nolint:errcheck // test
	}))
	defer srv.Close()

	tika := NewTika(srv.URL, nil)
	if _, err := tika.Extract(context.Background(), []byte("data")); err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if gotOCRLang != "" {
		t.Errorf("X-Tika-OCRLanguage = %q, want empty when languages is nil", gotOCRLang)
	}
}

func TestTika_Extract_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tika := NewTika(srv.URL, nil)
	_, err := tika.Extract(context.Background(), []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTika_Extract_ConnectionError(t *testing.T) {
	tika := NewTika("http://localhost:59999", nil)
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

	tika := NewTika(srv.URL, nil)
	if !tika.Available(context.Background()) {
		t.Error("expected available")
	}
}

func TestTika_NotAvailable(t *testing.T) {
	tika := NewTika("http://localhost:59999", nil)
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

	r := NewRegistry(srv.URL, nil)
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
	r := NewRegistry("", nil)
	text, err := r.Extract(context.Background(), "text/plain", []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Errorf("expected 'hello world', got %q", text)
	}
}

func TestRegistry_CanExtract(t *testing.T) {
	r := NewRegistry("", nil)
	if !r.CanExtract("text/plain") {
		t.Error("expected plain text to be extractable")
	}
	if r.CanExtract("application/pdf") {
		t.Error("expected PDF not extractable without Tika")
	}
}

func TestRegistry_NoExtractor(t *testing.T) {
	r := NewRegistry("", nil)
	_, err := r.Extract(context.Background(), "application/pdf", []byte("data"))
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}
