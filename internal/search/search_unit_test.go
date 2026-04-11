package search

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
)

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
