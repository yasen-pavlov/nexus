package extractor

import (
	"context"
	"strings"
)

type PlainText struct{}

func (p *PlainText) CanExtract(contentType string) bool {
	return contentType == "text/plain" || contentType == "text/markdown"
}

func (p *PlainText) Extract(_ context.Context, raw []byte) (string, error) {
	s := string(raw)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	return s, nil
}
