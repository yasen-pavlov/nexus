package extractor

import (
	"context"
	"testing"
)

func TestPlainText_CanExtract(t *testing.T) {
	p := &PlainText{}

	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/plain", true},
		{"text/markdown", true},
		{"application/pdf", false},
		{"image/png", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			if got := p.CanExtract(tt.contentType); got != tt.want {
				t.Errorf("CanExtract(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestPlainText_Extract(t *testing.T) {
	p := &PlainText{}
	ctx := context.Background()

	t.Run("trims whitespace", func(t *testing.T) {
		got, err := p.Extract(ctx, []byte("  hello world  \n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "hello world" {
			t.Errorf("expected 'hello world', got %q", got)
		}
	})

	t.Run("normalizes line endings", func(t *testing.T) {
		got, err := p.Extract(ctx, []byte("line1\r\nline2\r\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "line1\nline2" {
			t.Errorf("expected normalized newlines, got %q", got)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got, err := p.Extract(ctx, []byte(""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}
