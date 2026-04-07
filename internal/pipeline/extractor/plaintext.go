package extractor

import (
	"context"
	"strings"
)

// PlainText extracts text from plain text and markdown files.
type PlainText struct{}

func (p *PlainText) CanExtract(contentType string) bool {
	return strings.HasPrefix(contentType, "text/plain") || strings.HasPrefix(contentType, "text/markdown")
}

func (p *PlainText) Extract(_ context.Context, raw []byte) (string, error) {
	s := string(raw)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	return s, nil
}
