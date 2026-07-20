package extractor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/muty/nexus/internal/lang"
)

// defaultTikaTimeout is the extract-path HTTP client timeout. It is generous
// because Tesseract OCR through Tika runs several seconds per page, so a
// multi-page scanned PDF — exactly the document the Tika+OCR path exists for —
// routinely exceeds a short timeout, returns an error, and gets indexed with no
// content (the sync cursor advances, so the failure never self-heals).
// Override per-deployment via NEXUS_TIKA_TIMEOUT.
const defaultTikaTimeout = 5 * time.Minute

// availTimeout is the (short) timeout for the Available() health probe — it
// must fail fast, unlike the extract path.
const availTimeout = 10 * time.Second

// Tika extracts text content from binary files using an Apache Tika server.
type Tika struct {
	url         string
	client      *http.Client
	availClient *http.Client
	ocrLanguage string // value for X-Tika-OCRLanguage header, e.g. "eng+deu+bul"
}

// TikaOption customizes a Tika extractor.
type TikaOption func(*Tika)

// WithTimeout overrides the extract-path HTTP client timeout. A value <= 0
// leaves the default in place.
func WithTimeout(d time.Duration) TikaOption {
	return func(t *Tika) {
		if d > 0 {
			t.client.Timeout = d
		}
	}
}

// NewTika creates a Tika extractor pointing at the given Tika server URL.
// languages configures the X-Tika-OCRLanguage header sent on every
// extract request so Tesseract uses the right language packs when OCR'ing
// scanned PDFs and images. An empty list omits the header entirely,
// leaving Tika to fall back to its default (English only).
func NewTika(url string, languages []lang.Language, opts ...TikaOption) *Tika {
	t := &Tika{
		url:         strings.TrimRight(url, "/"),
		client:      &http.Client{Timeout: defaultTikaTimeout},
		availClient: &http.Client{Timeout: availTimeout},
		ocrLanguage: lang.TesseractHeader(languages),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *Tika) CanExtract(contentType string) bool {
	// Tika handles almost everything except plain text (which PlainText handles better)
	switch {
	case strings.HasPrefix(contentType, "text/plain"):
		return false
	case strings.HasPrefix(contentType, "text/markdown"):
		return false
	case contentType == "":
		return false
	default:
		return true
	}
}

func (t *Tika) Extract(ctx context.Context, raw []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.url+"/tika", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("tika: create request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")
	if t.ocrLanguage != "" {
		req.Header.Set("X-Tika-OCRLanguage", t.ocrLanguage)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("tika: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tika: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("tika: read response: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}

// Available checks if the Tika server is reachable.
func (t *Tika) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/tika", nil)
	if err != nil {
		return false
	}
	resp, err := t.availClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body
	return resp.StatusCode == http.StatusOK
}
