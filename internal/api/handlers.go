package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rag"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/syncruns"
	"go.uber.org/zap"
)

// Search pagination ceilings. Together they bound how much OpenSearch is
// asked to retrieve+rank for a single API call. 1000 covers any realistic
// "load more" scroll on a homelab corpus; deep-paginating past 10k results
// is almost certainly a misuse pattern (or a scraper) and would be better
// served by tightening the query.
const (
	maxSearchLimit  = 1000
	maxSearchOffset = 10000
)

type handler struct {
	store         *store.Store
	search        *search.Client
	searchService *SearchService
	pipeline      *pipeline.Pipeline
	em            *EmbeddingManager
	rm            *RerankManager
	lm            *LLMManager
	ragMgr        *RAGManager
	rag           *rag.Orchestrator
	cm            *ConnectorManager
	syncJobs      *SyncJobManager
	binaryStore   *storage.BinaryStore
	sweeper       *syncruns.Sweeper
	ranking       *RankingManager
	jwtSecret     []byte
	revocation    *auth.TokenRevocationCache
	apiTokenAuth  *apiTokenAuthenticator
	log           *zap.Logger
	// loginLimiter throttles failed /auth/login attempts per
	// (username, ip) bucket to defend weak passwords against online
	// brute-force. Nil disables throttling (used in tests that don't
	// care about rate limiting).
	loginLimiter *auth.LoginRateLimiter
}

// Health godoc
//
//	@Summary	Health check
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Router		/health [get]
func (h *handler) Health(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"status": "ok"}
	if h.store != nil {
		count, err := h.store.CountUsers(r.Context())
		if err == nil && count == 0 {
			resp["setup_required"] = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// HealthReady godoc
//
//	@Summary	Readiness check
//	@Description	Probes the hard dependencies (Postgres + OpenSearch) required to serve search. Returns 503 with a per-component map when any is unreachable. Soft deps (Tika, embeddings, LLM) are excluded — BM25 search works without them.
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]any
//	@Failure	503	{object}	map[string]any
//	@Router		/health/ready [get]
func (h *handler) HealthReady(w http.ResponseWriter, r *http.Request) {
	components := map[string]string{}
	healthy := true

	if h.store != nil {
		if err := h.store.Ping(r.Context()); err != nil {
			components["postgres"] = "down"
			healthy = false
		} else {
			components["postgres"] = "ok"
		}
	}
	if h.search != nil {
		if err := h.search.Ping(r.Context()); err != nil {
			components["opensearch"] = "down"
			healthy = false
		} else {
			components["opensearch"] = "ok"
		}
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	// Use writeJSON (the {data:…} envelope) even on 503 so envelope-based
	// clients tolerate it — writeError would set an `error` field that the FE
	// api-client treats as a hard failure.
	writeJSON(w, code, map[string]any{"status": status, "components": components})
}

// Search godoc
//
//	@Summary	Search across all indexed documents
//	@Description	Performs hybrid search (BM25 + vector) if embeddings are enabled, otherwise BM25-only.
//	@Tags		search
//	@Produce	json
//	@Param		q				query	string	true	"Search query"
//	@Param		limit			query	int		false	"Max results (default 20)"
//	@Param		offset			query	int		false	"Pagination offset"
//	@Param		sources			query	string	false	"Filter by source types (comma-separated)"
//	@Param		source_names	query	string	false	"Filter by source names (comma-separated)"
//	@Param		date_from		query	string	false	"Filter by date (YYYY-MM-DD)"
//	@Param		date_to			query	string	false	"Filter by date (YYYY-MM-DD)"
//	@Success	200	{object}	model.SearchResult
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/search [get]
func (h *handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	req := buildSearchRequest(r, query)
	explain := r.URL.Query().Get("score_details") == "true"

	result, err := h.search2().Run(r.Context(), req, SearchOptions{IncludeScoreDetails: explain})
	if err != nil {
		h.log.Error("search failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	paginateSearchResult(result, req)
	writeJSON(w, http.StatusOK, result)
}

// search2 returns the configured SearchService, falling back to a
// services constructed from the handler's individual managers when
// nil. Tests that build &handler{...} directly hit the fallback;
// production goes through NewRouter which wires it explicitly.
func (h *handler) search2() *SearchService {
	if h.searchService != nil {
		return h.searchService
	}
	return NewSearchService(h.search, h.em, h.rm, h.ranking, h.log)
}

// buildSearchRequest parses and clamps the query-string pagination + filter
// parameters into a model.SearchRequest. Invalid inputs fall back to safe
// defaults — full validation would reject too much legitimate traffic.
func buildSearchRequest(r *http.Request, query string) model.SearchRequest {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}
	// Cap at maxSearchLimit so an attacker can't request millions of
	// results in a single call. Mirrors the maxConversationLimit pattern
	// in document_handlers.go.
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxSearchOffset {
		offset = maxSearchOffset
	}
	return model.SearchRequest{
		Query:       query,
		Limit:       limit,
		Offset:      offset,
		Sources:     parseCSV(r.URL.Query().Get("sources")),
		SourceNames: parseCSV(r.URL.Query().Get("source_names")),
		DateFrom:    r.URL.Query().Get("date_from"),
		DateTo:      r.URL.Query().Get("date_to"),
		OwnerID:     auth.UserIDFromContext(r.Context()).String(),
	}
}

// applySourceTrustWeights multiplies each hit's Rank by its per-source trust
// weight (defaulting to 1.0). No-op when no reranker ran or the feature is
// disabled — without a reranker, Rank is raw RRF which isn't meaningful to
// weight.
func applySourceTrustWeights(result *model.SearchResult, rankCfg search.RankingConfig, rerankerActive bool) {
	if !rerankerActive || !rankCfg.SourceTrustEnabled {
		return
	}
	for i := range result.Documents {
		w, ok := rankCfg.SourceTrustWeight[result.Documents[i].SourceType]
		if !ok {
			w = 1.0
		}
		result.Documents[i].Rank *= w
	}
}

// applyRerankerFloor drops hits whose reranked score falls below the
// configured floor. No-op when no reranker ran.
func applyRerankerFloor(result *model.SearchResult, rankCfg search.RankingConfig, rerankerActive bool) {
	if !rerankerActive {
		return
	}
	filtered := result.Documents[:0]
	for _, hit := range result.Documents {
		if hit.Rank >= rankCfg.RerankerMinScore {
			filtered = append(filtered, hit)
		}
	}
	result.Documents = filtered
}

// paginateSearchResult records the post-filter total onto TotalCount, then
// slices Documents down to the requested offset/limit page.
func paginateSearchResult(result *model.SearchResult, req model.SearchRequest) {
	result.TotalCount = len(result.Documents)
	if req.Offset > 0 && req.Offset < len(result.Documents) {
		result.Documents = result.Documents[req.Offset:]
	} else if req.Offset >= len(result.Documents) {
		result.Documents = nil
	}
	if len(result.Documents) > req.Limit {
		result.Documents = result.Documents[:req.Limit]
	}
}

// TriggerSync godoc
//
//	@Summary	Trigger sync for a single connector
//	@Description	Starts an async sync job. Returns immediately with the job state.
//	@Tags		sync
//	@Produce	json
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	202	{object}	SyncJob
//	@Failure	404	{object}	APIResponse
//	@Failure	409	{object}	APIResponse	"Sync already running"
//	@Security	BearerAuth
//	@Router		/sync/{id} [post]
func (h *handler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}
	conn, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	if !canModifyConnector(auth.UserFromContext(r.Context()), cfg) {
		writeMutationDenied(w, auth.UserFromContext(r.Context()), cfg)
		return
	}

	job, runCtx, err := h.syncJobs.Start(cfg.ID, cfg.Name, conn.Type())
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			writeError(w, http.StatusConflict, "sync already running for "+cfg.Name)
			return
		}
		h.log.Error("sync job start failed", zap.String("connector", cfg.Name), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to start sync")
		return
	}
	snapshot := *job // copy before goroutine can mutate it

	// Run pipeline in background. runCtx is cancellable via
	// syncJobs.Cancel(job.ID) and propagates through conn.Fetch +
	// pipeline's per-doc loop.
	go func() {
		progress := func(total, processed, errors int, scope string) {
			h.syncJobs.Update(job.ID, total, processed, errors, scope)
		}

		ownerID := ""
		if cfg.UserID != nil {
			ownerID = cfg.UserID.String()
		}
		report, err := h.pipeline.RunWithProgress(runCtx, cfg.ID, conn, ownerID, cfg.Shared, progress)
		if report != nil {
			h.syncJobs.SetDeleted(job.ID, report.DocsDeleted)
		}
		h.syncJobs.Complete(job.ID, err)

		if err != nil && !errors.Is(err, context.Canceled) {
			h.log.Error("async sync failed", zap.String("connector", cfg.Name), zap.Error(err))
		}
	}()

	writeJSON(w, http.StatusAccepted, &snapshot)
}

// StreamSyncProgress godoc
//
//	@Summary	Stream sync progress via SSE
//	@Description	Opens a Server-Sent Events stream that pushes SyncJob updates in real-time. Sends an "event: done" when the job completes.
//	@Tags		sync
//	@Produce	text/event-stream
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	200	{string}	string	"SSE stream"
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/sync/{id}/progress [get]
func (h *handler) StreamSyncProgress(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}

	_, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	if !canReadConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	job := h.syncJobs.GetByConnector(cfg.ID)
	if job == nil {
		writeError(w, http.StatusNotFound, "no active sync for "+cfg.Name)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.syncJobs.Subscribe(job.ID)

	for {
		select {
		case update, open := <-ch:
			if !open {
				// Channel closed — job is done, send final event
				_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			data, _ := json.Marshal(update) //nolint:errcheck // best-effort
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ListSyncJobs godoc
//
//	@Summary	List active and recent sync jobs
//	@Tags		sync
//	@Produce	json
//	@Success	200	{array}	SyncJob
//	@Security	BearerAuth
//	@Router		/sync [get]
func (h *handler) ListSyncJobs(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	all := h.syncJobs.Active()
	visible := make([]*SyncJob, 0, len(all))
	for _, job := range all {
		connID, err := uuid.Parse(job.ConnectorID)
		if err != nil {
			continue
		}
		_, cfg, ok := h.cm.GetByID(connID)
		if !ok {
			continue
		}
		if canReadConnector(claims, cfg) {
			visible = append(visible, job)
		}
	}
	writeJSON(w, http.StatusOK, visible)
}

// DeleteAllCursors godoc
//
//	@Summary	Delete all sync cursors
//	@Description	Clears all sync cursors, forcing a full re-sync on next trigger.
//	@Tags		sync
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Failure	500	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/sync/cursors [delete]
func (h *handler) DeleteAllCursors(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteAllSyncCursors(r.Context()); err != nil {
		h.log.Error("delete all cursors failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete cursors")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "all cursors deleted"})
}

// DeleteCursor godoc
//
//	@Summary	Delete a single connector's sync cursor
//	@Tags		sync
//	@Produce	json
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	200	{object}	map[string]string
//	@Failure	500	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/sync/cursors/{id} [delete]
func (h *handler) DeleteCursor(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}
	_, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}
	if !canModifyConnector(auth.UserFromContext(r.Context()), cfg) {
		writeMutationDenied(w, auth.UserFromContext(r.Context()), cfg)
		return
	}
	if err := h.store.DeleteSyncCursor(r.Context(), id); err != nil {
		h.log.Error("delete cursor failed", zap.String("connector", cfg.Name), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete cursor")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "cursor deleted for " + cfg.Name})
}

// SyncAll godoc
//
//	@Summary	Sync all enabled connectors
//	@Description	Starts async sync jobs for all enabled connectors. Skips connectors that are already syncing.
//	@Tags		sync
//	@Produce	json
//	@Success	202	{array}	SyncJob
//	@Security	BearerAuth
//	@Router		/sync [post]
func (h *handler) SyncAll(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	jobs := []*SyncJob{}
	for connID, entry := range h.cm.All() {
		cfg := entry.Config
		if !canModifyConnector(claims, &cfg) {
			continue // user cannot trigger sync on this connector
		}
		job := h.startBatchSyncJob(connID, entry, "sync all")
		if job == nil {
			continue
		}
		// job is already a private snapshot taken before the run goroutine
		// started (see startBatchSyncJob), so it's safe to hand to the response.
		jobs = append(jobs, job)
	}
	writeJSON(w, http.StatusAccepted, jobs)
}

// startBatchSyncJob starts a sync job for a connector as part of a batch
// operation (SyncAll/TriggerReindex), logs failures with the given prefix and
// skips already-running connectors. Returns the started job, or nil if the
// connector couldn't be started.
func (h *handler) startBatchSyncJob(connID uuid.UUID, entry ConnectorWithConfig, logPrefix string) *SyncJob {
	connName := entry.Conn.Name()
	ownerID := ""
	if entry.Config.UserID != nil {
		ownerID = entry.Config.UserID.String()
	}
	job, runCtx, err := h.syncJobs.Start(connID, connName, entry.Conn.Type())
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			return nil // silently skip connectors already syncing
		}
		h.log.Error(logPrefix+": start failed", zap.String("connector", connName), zap.Error(err))
		return nil
	}
	// Snapshot the freshly-created job before the run goroutine starts
	// mutating it via syncJobs.Update — the caller may read the returned job
	// for the HTTP response, which would otherwise race the progress writes.
	snapshot := *job
	go h.runBatchSyncJob(connID, runCtx, connName, entry.Conn, ownerID, entry.Config.Shared, job.ID, logPrefix)
	return &snapshot
}

// runBatchSyncJob drives a single connector sync inside the pipeline and
// records progress/completion on the syncJobs registry.
func (h *handler) runBatchSyncJob(cid uuid.UUID, ctx context.Context, name string, c connector.Connector, ownerID string, shared bool, jobID string, logPrefix string) {
	progress := func(total, processed, errCount int, scope string) {
		h.syncJobs.Update(jobID, total, processed, errCount, scope)
	}
	report, err := h.pipeline.RunWithProgress(ctx, cid, c, ownerID, shared, progress)
	if report != nil {
		h.syncJobs.SetDeleted(jobID, report.DocsDeleted)
	}
	h.syncJobs.Complete(jobID, err)
	if err != nil && !errors.Is(err, context.Canceled) {
		h.log.Error(logPrefix+": connector failed", zap.String("connector", name), zap.Error(err))
	}
}

// TriggerReindex godoc
//
//	@Summary	Full re-index
//	@Description	Recreates the OpenSearch index with the current embedding dimension, clears all sync cursors, and triggers a full sync for all enabled connectors.
//	@Tags		sync
//	@Produce	json
//	@Success	202	{object}	map[string]any
//	@Failure	500	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/reindex [post]
func (h *handler) TriggerReindex(w http.ResponseWriter, r *http.Request) {
	// 1. Recreate index with current dimension
	dim := h.em.Dimension()
	if err := h.search.RecreateIndex(r.Context(), dim); err != nil {
		h.log.Error("reindex: recreate index failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to recreate index")
		return
	}

	// 2. Delete all cursors
	if err := h.store.DeleteAllSyncCursors(r.Context()); err != nil {
		h.log.Error("reindex: delete cursors failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete cursors")
		return
	}

	// 3. Sync all connectors
	var count int
	for connID, entry := range h.cm.All() {
		if h.startBatchSyncJob(connID, entry, "reindex") != nil {
			count++
		}
	}

	h.log.Info("reindex started", zap.Int("dimension", dim), zap.Int("connectors", count))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":    "reindex started",
		"dimension":  dim,
		"connectors": count,
	})
}

// GetStorageStats godoc
//
//	@Summary		Binary cache stats per connector
//	@Description	Returns per-source-type/name aggregates of cached binaries (count, total bytes). Admin only.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	APIResponse{data=[]model.BinaryStoreStats}
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/storage/stats [get]
func (h *handler) GetStorageStats(w http.ResponseWriter, r *http.Request) {
	if h.binaryStore == nil {
		writeJSON(w, http.StatusOK, []model.BinaryStoreStats{})
		return
	}
	stats, err := h.binaryStore.Stats(r.Context())
	if err != nil {
		h.log.Error("storage stats failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to fetch storage stats")
		return
	}
	if stats == nil {
		stats = []model.BinaryStoreStats{}
	}
	writeJSON(w, http.StatusOK, stats)
}

// summarizeStats folds a list of per-source stats into a single
// deleted_count / bytes_freed pair for the cache-delete endpoints'
// response body.
func summarizeStats(stats []model.BinaryStoreStats) (int64, int64) {
	var count, total int64
	for _, s := range stats {
		count += s.Count
		total += s.TotalSize
	}
	return count, total
}

// DeleteStorageCache godoc
//
//	@Summary		Wipe the entire binary cache
//	@Description	Deletes every cached blob across all connectors. Admin only. Eager-cached data (Telegram) will be re-populated on next sync only if the upstream media is still available; use with care.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	map[string]any
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/storage/cache [delete]
func (h *handler) DeleteStorageCache(w http.ResponseWriter, r *http.Request) {
	if h.binaryStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{"deleted_count": 0, "bytes_freed": 0})
		return
	}
	// Capture size before deletion so the response can report what was
	// freed. Stats-then-delete is racy with concurrent Puts, but this is
	// an admin operation that the admin just triggered, so close enough.
	stats, err := h.binaryStore.Stats(r.Context())
	if err != nil {
		h.log.Error("storage cache delete: stats failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to summarize cache before delete")
		return
	}
	if err := h.binaryStore.DeleteAll(r.Context()); err != nil {
		h.log.Error("storage cache delete: wipe failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to wipe cache")
		return
	}
	count, bytesFreed := summarizeStats(stats)
	h.log.Info("storage cache wiped", zap.Int64("deleted_count", count), zap.Int64("bytes_freed", bytesFreed))
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted_count": count,
		"bytes_freed":   bytesFreed,
	})
}

// DeleteStorageCacheByConnector godoc
//
//	@Summary		Wipe the binary cache for a single connector
//	@Description	Deletes every cached blob for the connector identified by path ID. Admin only. Safe for lazy-mode connectors — they'll repopulate the cache on next preview. Eager-mode connectors lose cached data that may not be re-fetchable if the upstream source has expired.
//	@Tags			settings
//	@Param			id	path	string	true	"Connector UUID"
//	@Produce		json
//	@Success		200	{object}	map[string]any
//	@Failure		400	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/storage/cache/{id} [delete]
func (h *handler) DeleteStorageCacheByConnector(w http.ResponseWriter, r *http.Request) {
	if h.binaryStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{"deleted_count": 0, "bytes_freed": 0})
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}
	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		h.log.Error("storage cache delete: get connector failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to resolve connector")
		return
	}

	// Snapshot stats for this source before deletion so we can report
	// what was freed. Only the matching aggregate is relevant.
	stats, err := h.binaryStore.Stats(r.Context())
	if err != nil {
		h.log.Error("storage cache delete: stats failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to summarize cache before delete")
		return
	}
	var filtered []model.BinaryStoreStats
	for _, s := range stats {
		if s.SourceType == cfg.Type && s.SourceName == cfg.Name {
			filtered = append(filtered, s)
		}
	}

	if err := h.binaryStore.DeleteBySource(r.Context(), cfg.Type, cfg.Name); err != nil {
		h.log.Error("storage cache delete by connector failed",
			zap.String("type", cfg.Type),
			zap.String("name", cfg.Name),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "failed to wipe connector cache")
		return
	}
	count, bytesFreed := summarizeStats(filtered)
	h.log.Info("storage cache wiped for connector",
		zap.String("type", cfg.Type),
		zap.String("name", cfg.Name),
		zap.Int64("deleted_count", count),
		zap.Int64("bytes_freed", bytesFreed),
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted_count": count,
		"bytes_freed":   bytesFreed,
	})
}

// dedupeNearDuplicates drops documents whose first 200 chars of
// (title + content) are identical to an earlier doc in the slice. The first
// occurrence wins because the input is sorted by pre-rerank rank already.
// This is a conservative heuristic — it only catches exact prefix matches —
// but it's enough to remove the common case of one newsletter producing
// multiple chunks that all share the same boilerplate header.
func dedupeNearDuplicates(docs []model.DocumentHit) []model.DocumentHit {
	if len(docs) <= 1 {
		return docs
	}
	const fingerprintLen = 200
	seen := make(map[uint64]struct{}, len(docs))
	out := docs[:0]
	for _, doc := range docs {
		text := strings.ToLower(strings.TrimSpace(doc.Title + " " + doc.Content))
		if len(text) > fingerprintLen {
			text = text[:fingerprintLen]
		}
		h := fnv.New64a()
		h.Write([]byte(text)) //nolint:errcheck // hash.Hash64.Write never returns an error
		fp := h.Sum64()
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		out = append(out, doc)
	}
	return out
}

func reorderByRerankScores(result *model.SearchResult, ranked []rerank.Result) *model.SearchResult {
	reordered := make([]model.DocumentHit, 0, len(ranked))
	for _, r := range ranked {
		if r.Index >= 0 && r.Index < len(result.Documents) {
			hit := result.Documents[r.Index]
			hit.Rank = r.Score
			reordered = append(reordered, hit)
		}
	}
	result.Documents = reordered
	return result
}

func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
