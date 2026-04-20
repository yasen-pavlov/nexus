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

func TestBuildFilterClauses_OwnerID(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{OwnerID: "user-123"})
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}

	bool1, ok := filters[0]["bool"].(map[string]any)
	if !ok {
		t.Fatal("expected bool wrapper for ownership filter")
	}
	should, ok := bool1["should"].([]map[string]any)
	if !ok {
		t.Fatal("expected should clauses")
	}
	if len(should) != 2 {
		t.Errorf("expected 2 should clauses (owner_id match, shared), got %d", len(should))
	}
	if min, _ := bool1["minimum_should_match"].(int); min != 1 {
		t.Errorf("expected minimum_should_match=1, got %v", bool1["minimum_should_match"])
	}

	var sawOwner, sawShared bool
	for _, clause := range should {
		term, ok := clause["term"].(map[string]any)
		if !ok {
			continue
		}
		if v, ok := term["owner_id"].(string); ok && v == "user-123" {
			sawOwner = true
		}
		if v, ok := term["shared"].(bool); ok && v {
			sawShared = true
		}
	}
	if !sawOwner {
		t.Error("missing owner_id term clause")
	}
	if !sawShared {
		t.Error("missing shared=true clause")
	}
}

func TestBuildFilterClauses_OwnerIDEmpty(t *testing.T) {
	// Empty OwnerID = no ownership filter (e.g., admin-level system query)
	filters := buildFilterClauses(model.SearchRequest{Query: "anything"})
	if len(filters) != 0 {
		t.Errorf("expected no filters for empty OwnerID, got %d", len(filters))
	}
}

func TestBuildFilterClauses_OwnerIDPlusSources(t *testing.T) {
	filters := buildFilterClauses(model.SearchRequest{
		OwnerID: "user-456",
		Sources: []string{"paperless"},
	})
	if len(filters) != 2 {
		t.Errorf("expected 2 filters (sources + ownership), got %d", len(filters))
	}
}

func TestBuildSearchQuery_AlwaysExcludesHidden(t *testing.T) {
	// buildSearchQuery must always wrap in a bool with a must_not for
	// hidden=true, so Telegram per-message docs (and anything else that
	// opts out of default search) never surface. This applies regardless
	// of whether filter clauses are also present.
	match := map[string]any{"match_all": map[string]any{}}
	q := buildSearchQuery(match, nil)
	boolClause, ok := q["bool"].(map[string]any)
	if !ok {
		t.Fatal("expected bool wrapper even with no filters")
	}
	mustNot, ok := boolClause["must_not"].([]map[string]any)
	if !ok || len(mustNot) == 0 {
		t.Fatalf("expected must_not to exclude hidden docs, got %v", boolClause)
	}
	term, ok := mustNot[0]["term"].(map[string]any)
	if !ok || term["hidden"] != true {
		t.Errorf("must_not clause should be {term: {hidden: true}}, got %v", mustNot[0])
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
