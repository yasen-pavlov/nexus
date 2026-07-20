package extractor

import (
	"context"
	"fmt"

	"github.com/muty/nexus/internal/lang"
)

// Registry chains multiple extractors, trying each in order.
type Registry struct {
	extractors []Extractor
}

// NewRegistry creates an extractor registry.
// If tikaURL is provided, Tika is added as a fallback extractor. languages
// drives the X-Tika-OCRLanguage header so Tesseract knows which language packs
// to use when OCR'ing scanned PDFs and images.
//
// Tika is registered UNCONDITIONALLY (no boot-time Available() probe): a single
// startup probe would permanently disable binary extraction for the whole
// process if Tika happened to be unreachable at that instant (a restart while
// Tika is briefly down, `make dev` without the container, Tika crash-looping at
// boot). Extract already surfaces per-request errors cleanly and every consumer
// falls back to empty content on error, so a Tika-down state yields the same
// output as before — but the moment Tika recovers, the next document extracts
// correctly with no nexus restart required.
func NewRegistry(tikaURL string, languages []lang.Language, opts ...TikaOption) *Registry {
	r := &Registry{
		extractors: []Extractor{&PlainText{}},
	}

	if tikaURL != "" {
		r.extractors = append(r.extractors, NewTika(tikaURL, languages, opts...))
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
