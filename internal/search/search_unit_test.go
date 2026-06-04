package search

import (
	"context"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
)

func TestBulkItemFailures(t *testing.T) {
	items := []map[string]opensearchapi.BulkRespItem{
		{"index": {ID: "a", Status: 201, Result: "created"}},
		{"index": {ID: "b", Status: 400}}, // failed item (4xx, no Error body)
		{"index": {ID: "c", Status: 200, Result: "updated"}},
	}
	failed, sample := bulkItemFailures(items)
	if failed != 1 {
		t.Fatalf("failed=%d want 1", failed)
	}
	if !strings.Contains(sample, "id=b") || !strings.Contains(sample, "status=400") {
		t.Errorf("sample=%q want it to identify the failed item", sample)
	}
}

func TestBulkItemFailures_AllOK(t *testing.T) {
	items := []map[string]opensearchapi.BulkRespItem{
		{"index": {ID: "a", Status: 201}},
		{"index": {ID: "b", Status: 200}},
	}
	failed, _ := bulkItemFailures(items)
	if failed != 0 {
		t.Fatalf("failed=%d want 0", failed)
	}
}

func TestNew_ConnectionError(t *testing.T) {
	_, err := New(context.Background(), "http://localhost:59999", nil, lang.Default())
	if err == nil {
		t.Fatal("expected error for unreachable OpenSearch")
	}
}

func TestNewWithIndex_ConnectionError(t *testing.T) {
	_, err := NewWithIndex(context.Background(), "http://localhost:59999", "test", nil, lang.Default())
	if err == nil {
		t.Fatal("expected error for unreachable OpenSearch")
	}
}

func TestHighlightConfig_HTMLEncoder(t *testing.T) {
	// encoder=html makes OpenSearch escape fragment text so markup in indexed
	// content can't inject active HTML when the headline is rendered. Without
	// it, a document containing <img onerror=...> would yield an executable
	// fragment. See sanitizeHighlight on the frontend for the second layer.
	c := &Client{languages: lang.Default()}
	cfg := c.highlightConfig()
	if cfg["encoder"] != "html" {
		t.Fatalf("highlightConfig encoder = %v, want \"html\"", cfg["encoder"])
	}
}

func TestDocID(t *testing.T) {
	tests := []struct {
		sourceType, sourceName, sourceID, want string
	}{
		{"filesystem", "test", "file.txt", "filesystem:test:file.txt"},
		{"paperless", "docs", "123", "paperless:docs:123"},
	}
	for _, tt := range tests {
		doc := &model.Document{SourceType: tt.sourceType, SourceName: tt.sourceName, SourceID: tt.sourceID}
		if got := docID(doc); got != tt.want {
			t.Errorf("docID() = %q, want %q", got, tt.want)
		}
	}
}
