package search

import (
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func TestBuildFilterClauses_Empty(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{Query: "test"})
	if len(filters) != 0 {
		t.Errorf("expected 0 filters, got %d", len(filters))
	}
}

func TestBuildFilterClauses_Sources(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{Sources: []string{"paperless", "filesystem"}})
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
}

func TestBuildFilterClauses_AllFilters(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{
		Sources:     []string{"paperless"},
		SourceNames: []string{"my-docs"},
		DateFrom:    "2025-01-01",
		DateTo:      "2025-12-31",
	})
	if len(filters) != 3 {
		t.Errorf("expected 3 filters, got %d", len(filters))
	}
}

func TestBuildFilterClauses_DateFromOnly(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{DateFrom: "2025-01-01"})
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
}

func TestBuildSearchQuery_NoFilters(t *testing.T) {
	match := map[string]any{"match_all": map[string]any{}}
	q := buildSearchQuery(match, nil)
	if _, ok := q["bool"]; ok {
		t.Error("expected no bool wrapper with no filters")
	}
}

func TestBuildSearchQuery_WithFilters(t *testing.T) {
	match := map[string]any{"match_all": map[string]any{}}
	filters := []map[string]any{{"term": map[string]any{"source_type": "test"}}}
	q := buildSearchQuery(match, filters)
	if _, ok := q["bool"]; !ok {
		t.Error("expected bool wrapper with filters")
	}
}

func TestComputeFacets(t *testing.T) {
	now := time.Now()
	results := []*rankedChunk{
		{doc: model.Document{SourceType: "paperless", SourceName: "docs", CreatedAt: now}},
		{doc: model.Document{SourceType: "paperless", SourceName: "docs", CreatedAt: now}},
		{doc: model.Document{SourceType: "filesystem", SourceName: "files", CreatedAt: now}},
	}

	facets := computeFacets(results)

	if len(facets["source_type"]) != 2 {
		t.Fatalf("expected 2 source_type facets, got %d", len(facets["source_type"]))
	}

	// Find paperless count
	var paperlessCount int
	for _, f := range facets["source_type"] {
		if f.Value == "paperless" {
			paperlessCount = f.Count
		}
	}
	if paperlessCount != 2 {
		t.Errorf("expected paperless count 2, got %d", paperlessCount)
	}

	if len(facets["source_name"]) != 2 {
		t.Errorf("expected 2 source_name facets, got %d", len(facets["source_name"]))
	}
}

func TestComputeFacets_Empty(t *testing.T) {
	facets := computeFacets(nil)
	if facets != nil {
		t.Error("expected nil for empty results")
	}
}
