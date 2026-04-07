package extractor

import (
	"context"
	"fmt"
)

// Registry chains multiple extractors, trying each in order.
type Registry struct {
	extractors []Extractor
}

// NewRegistry creates an extractor registry.
// If tikaURL is provided and Tika is available, it's added as a fallback extractor.
func NewRegistry(tikaURL string) *Registry {
	r := &Registry{
		extractors: []Extractor{&PlainText{}},
	}

	if tikaURL != "" {
		tika := NewTika(tikaURL)
		if tika.Available(context.Background()) {
			r.extractors = append(r.extractors, tika)
		}
	}

	return r
}

// Extract tries each registered extractor in order and returns the first successful result.
func (r *Registry) Extract(ctx context.Context, contentType string, raw []byte) (string, error) {
	for _, ext := range r.extractors {
		if ext.CanExtract(contentType) {
			return ext.Extract(ctx, raw)
		}
	}
	return "", fmt.Errorf("no extractor available for content type %q", contentType)
}

// CanExtract returns true if any registered extractor can handle the content type.
func (r *Registry) CanExtract(contentType string) bool {
	for _, ext := range r.extractors {
		if ext.CanExtract(contentType) {
			return true
		}
	}
	return false
}
