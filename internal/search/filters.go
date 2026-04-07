package search

import "github.com/muty/nexus/internal/model"

// buildFilterClauses returns OpenSearch filter clauses for the given search request.
func buildFilterClauses(req model.SearchRequest) []map[string]any {
	var filters []map[string]any

	if len(req.Sources) > 0 {
		filters = append(filters, map[string]any{
			"terms": map[string]any{"source_type": req.Sources},
		})
	}

	if len(req.SourceNames) > 0 {
		filters = append(filters, map[string]any{
			"terms": map[string]any{"source_name": req.SourceNames},
		})
	}

	if req.DateFrom != "" || req.DateTo != "" {
		rangeFilter := map[string]any{}
		if req.DateFrom != "" {
			rangeFilter["gte"] = req.DateFrom
		}
		if req.DateTo != "" {
			rangeFilter["lte"] = req.DateTo
		}
		filters = append(filters, map[string]any{
			"range": map[string]any{"created_at": rangeFilter},
		})
	}

	return filters
}

// buildSearchQuery wraps a match query with optional filters.
func buildSearchQuery(matchQuery map[string]any, filters []map[string]any) map[string]any {
	if len(filters) == 0 {
		return matchQuery
	}
	return map[string]any{
		"bool": map[string]any{
			"must":   matchQuery,
			"filter": filters,
		},
	}
}

// computeFacets counts source_type and source_name across all deduped results.
func computeFacets(results []*rankedChunk) map[string][]model.Facet {
	if len(results) == 0 {
		return nil
	}

	typeCounts := make(map[string]int)
	nameCounts := make(map[string]int)

	for _, r := range results {
		typeCounts[r.doc.SourceType]++
		nameCounts[r.doc.SourceName]++
	}

	facets := make(map[string][]model.Facet)

	if len(typeCounts) > 0 {
		var types []model.Facet
		for v, c := range typeCounts {
			types = append(types, model.Facet{Value: v, Count: c})
		}
		facets["source_type"] = types
	}

	if len(nameCounts) > 0 {
		var names []model.Facet
		for v, c := range nameCounts {
			names = append(names, model.Facet{Value: v, Count: c})
		}
		facets["source_name"] = names
	}

	return facets
}
