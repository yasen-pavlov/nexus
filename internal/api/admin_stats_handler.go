package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/muty/nexus/internal/search"
	"go.uber.org/zap"
)

// AdminStats is the response payload for `GET /api/admin/stats`. It joins
// OpenSearch aggregates with the binary cache stats and the currently-loaded
// embedding/rerank providers so the admin UI can render the whole dashboard
// from a single request.
type AdminStats struct {
	TotalDocuments int64                 `json:"total_documents"`
	TotalChunks    int64                 `json:"total_chunks"`
	UsersCount     int                   `json:"users_count"`
	PerSource      []AdminPerSourceStats `json:"per_source"`
	Embedding      AdminEngineStats      `json:"embedding"`
	Rerank         AdminEngineStats      `json:"rerank"`
}

// AdminPerSourceStats summarises one (source_type, source_name) in both the
// search index and the binary cache.
type AdminPerSourceStats struct {
	SourceType      string     `json:"source_type"`
	SourceName      string     `json:"source_name"`
	DocumentCount   int64      `json:"document_count"`
	ChunkCount      int64      `json:"chunk_count"`
	LatestIndexedAt *time.Time `json:"latest_indexed_at,omitempty"`
	FirstIndexedAt  *time.Time `json:"first_indexed_at,omitempty"`
	CacheCount      int64      `json:"cache_count"`
	CacheBytes      int64      `json:"cache_bytes"`
}

// AdminEngineStats describes the currently-active embedding or rerank engine.
// Dimension is 0 when irrelevant (rerank) or when the provider is disabled.
type AdminEngineStats struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension,omitempty"`
}

// GetAdminStats godoc
//
//	@Summary		System-wide statistics
//	@Description	Aggregates document counts, cache footprint, and engine configuration for the admin dashboard. Admin only.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	AdminStats
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/admin/stats [get]
func (h *handler) GetAdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	aggs, totalChunks, err := h.search.AggregateByTypeAndName(ctx)
	if err != nil {
		h.log.Error("admin stats: aggregate by source", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to aggregate search index")
		return
	}

	cacheBySource, cacheCountBySource, err := h.loadCacheBySource(ctx)
	if err != nil {
		h.log.Error("admin stats: binary stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load cache stats")
		return
	}

	usersCount, err := h.store.CountUsers(ctx)
	if err != nil {
		h.log.Error("admin stats: count users", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to count users")
		return
	}

	perSource, totalDocs := buildPerSourceStats(aggs, cacheBySource, cacheCountBySource)
	sortPerSourceStats(perSource)

	resp := AdminStats{
		TotalDocuments: totalDocs,
		TotalChunks:    totalChunks,
		UsersCount:     usersCount,
		PerSource:      perSource,
		Embedding: AdminEngineStats{
			Enabled:   h.em.Get() != nil,
			Provider:  h.em.Provider(),
			Model:     h.em.Model(),
			Dimension: h.em.Dimension(),
		},
		Rerank: AdminEngineStats{
			Enabled:  h.rm.Get() != nil,
			Provider: h.rm.Provider(),
			Model:    h.rm.Model(),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadCacheBySource fetches binary cache stats keyed by "sourceType/sourceName".
// Returns empty maps when the binary store isn't configured.
func (h *handler) loadCacheBySource(ctx context.Context) (map[string]int64, map[string]int64, error) {
	cacheBySource := map[string]int64{}
	cacheCountBySource := map[string]int64{}
	if h.binaryStore == nil {
		return cacheBySource, cacheCountBySource, nil
	}
	cacheStats, err := h.binaryStore.Stats(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range cacheStats {
		k := s.SourceType + "/" + s.SourceName
		cacheBySource[k] = s.TotalSize
		cacheCountBySource[k] = s.Count
	}
	return cacheBySource, cacheCountBySource, nil
}

// buildPerSourceStats converts OpenSearch aggregates into AdminPerSourceStats
// rows and returns the running total document count.
func buildPerSourceStats(aggs []search.SourceAggregate, cacheBytes, cacheCount map[string]int64) ([]AdminPerSourceStats, int64) {
	perSource := make([]AdminPerSourceStats, 0, len(aggs))
	var totalDocs int64
	for _, a := range aggs {
		k := a.SourceType + "/" + a.SourceName
		var latest, first *time.Time
		if !a.MaxIndexedAt.IsZero() {
			t := a.MaxIndexedAt
			latest = &t
		}
		if !a.MinIndexedAt.IsZero() {
			t := a.MinIndexedAt
			first = &t
		}
		perSource = append(perSource, AdminPerSourceStats{
			SourceType:      a.SourceType,
			SourceName:      a.SourceName,
			DocumentCount:   a.DistinctCount,
			ChunkCount:      a.DocCount,
			LatestIndexedAt: latest,
			FirstIndexedAt:  first,
			CacheCount:      cacheCount[k],
			CacheBytes:      cacheBytes[k],
		})
		totalDocs += a.DistinctCount
	}
	return perSource, totalDocs
}

// sortPerSourceStats orders rows most-recent-first with a source-type/name
// tiebreaker so equal timestamps don't shuffle between requests.
func sortPerSourceStats(rows []AdminPerSourceStats) {
	sort.SliceStable(rows, func(i, j int) bool {
		li, lj := rows[i].LatestIndexedAt, rows[j].LatestIndexedAt
		switch {
		case li != nil && lj != nil && !li.Equal(*lj):
			return li.After(*lj)
		case li != nil && lj == nil:
			return true
		case li == nil && lj != nil:
			return false
		}
		if rows[i].SourceType != rows[j].SourceType {
			return rows[i].SourceType < rows[j].SourceType
		}
		return rows[i].SourceName < rows[j].SourceName
	})
}
