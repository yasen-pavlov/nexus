package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

type handler struct {
	store    *store.Store
	search   *search.Client
	pipeline *pipeline.Pipeline
	em       *EmbeddingManager
	rm       *RerankManager
	cm       *ConnectorManager
	syncJobs *SyncJobManager
	log      *zap.Logger
}

// Health godoc
//
//	@Summary	Health check
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Router		/health [get]
func (h *handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
//	@Router		/search [get]
func (h *handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	req := model.SearchRequest{
		Query:       query,
		Limit:       limit,
		Offset:      offset,
		Sources:     parseCSV(r.URL.Query().Get("sources")),
		SourceNames: parseCSV(r.URL.Query().Get("source_names")),
		DateFrom:    r.URL.Query().Get("date_from"),
		DateTo:      r.URL.Query().Get("date_to"),
	}

	// Try hybrid search if embedder is available
	var result *model.SearchResult
	embedder := h.em.Get()
	if embedder != nil {
		embeddings, err := embedder.Embed(r.Context(), []string{query})
		if err == nil && len(embeddings) > 0 {
			result, err = h.search.HybridSearch(r.Context(), req, embeddings[0])
			if err != nil {
				h.log.Warn("hybrid search failed, falling back to BM25", zap.Error(err))
			}
		}
	}

	// Fallback: BM25-only
	if result == nil {
		var err error
		result, err = h.search.Search(r.Context(), req)
		if err != nil {
			h.log.Error("search failed", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "search failed")
			return
		}
	}

	// Rerank results if a reranker is available
	result = h.rerankResults(r.Context(), query, result)

	// Apply recency decay — boost recent documents, source-specific half-lives
	search.ApplyRecencyDecay(result)

	writeJSON(w, http.StatusOK, result)
}

// TriggerSync godoc
//
//	@Summary	Trigger sync for a single connector
//	@Description	Starts an async sync job. Returns immediately with the job state.
//	@Tags		sync
//	@Produce	json
//	@Param		connector	path	string	true	"Connector name"
//	@Success	202	{object}	SyncJob
//	@Failure	404	{object}	APIResponse
//	@Failure	409	{object}	APIResponse	"Sync already running"
//	@Router		/sync/{connector} [post]
func (h *handler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "connector")
	conn, ok := h.cm.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "connector not found: "+name)
		return
	}

	// Check if a sync is already running for this connector
	if existing := h.syncJobs.GetByConnector(name); existing != nil {
		writeError(w, http.StatusConflict, "sync already running for "+name)
		return
	}

	job := h.syncJobs.Start(name, conn.Type())
	snapshot := *job // copy before goroutine can mutate it

	// Run pipeline in background with a detached context
	go func() {
		ctx := context.Background()
		progress := func(total, processed, errors int) {
			h.syncJobs.Update(job.ID, total, processed, errors)
		}

		_, err := h.pipeline.RunWithProgress(ctx, conn, progress)
		h.syncJobs.Complete(job.ID, err)

		if err != nil {
			h.log.Error("async sync failed", zap.String("connector", name), zap.Error(err))
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
//	@Param		connector	path	string	true	"Connector name"
//	@Success	200	{string}	string	"SSE stream"
//	@Failure	404	{object}	APIResponse
//	@Router		/sync/{connector}/progress [get]
func (h *handler) StreamSyncProgress(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "connector")

	job := h.syncJobs.GetByConnector(name)
	if job == nil {
		writeError(w, http.StatusNotFound, "no active sync for "+name)
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
//	@Router		/sync [get]
func (h *handler) ListSyncJobs(w http.ResponseWriter, _ *http.Request) {
	jobs := h.syncJobs.Active()
	writeJSON(w, http.StatusOK, jobs)
}

// DeleteAllCursors godoc
//
//	@Summary	Delete all sync cursors
//	@Description	Clears all sync cursors, forcing a full re-sync on next trigger.
//	@Tags		sync
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Failure	500	{object}	APIResponse
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
//	@Param		connector	path	string	true	"Connector name"
//	@Success	200	{object}	map[string]string
//	@Failure	500	{object}	APIResponse
//	@Router		/sync/cursors/{connector} [delete]
func (h *handler) DeleteCursor(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "connector")
	if err := h.store.DeleteSyncCursor(r.Context(), name); err != nil {
		h.log.Error("delete cursor failed", zap.String("connector", name), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete cursor")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "cursor deleted for " + name})
}

// SyncAll godoc
//
//	@Summary	Sync all enabled connectors
//	@Description	Starts async sync jobs for all enabled connectors. Skips connectors that are already syncing.
//	@Tags		sync
//	@Produce	json
//	@Success	202	{array}	SyncJob
//	@Router		/sync [post]
func (h *handler) SyncAll(w http.ResponseWriter, _ *http.Request) {
	var jobs []*SyncJob
	for name, conn := range h.cm.All() {
		if existing := h.syncJobs.GetByConnector(name); existing != nil {
			continue // already running
		}
		job := h.syncJobs.Start(name, conn.Type())
		snapshot := *job
		jobs = append(jobs, &snapshot)

		go func(n string, c connector.Connector, jobID string) {
			ctx := context.Background()
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			_, err := h.pipeline.RunWithProgress(ctx, c, progress)
			h.syncJobs.Complete(jobID, err)
			if err != nil {
				h.log.Error("sync all: connector failed", zap.String("connector", n), zap.Error(err))
			}
		}(name, conn, job.ID)
	}
	writeJSON(w, http.StatusAccepted, jobs)
}

// TriggerReindex godoc
//
//	@Summary	Full re-index
//	@Description	Recreates the OpenSearch index with the current embedding dimension, clears all sync cursors, and triggers a full sync for all enabled connectors.
//	@Tags		sync
//	@Produce	json
//	@Success	202	{object}	map[string]any
//	@Failure	500	{object}	APIResponse
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
	for name, conn := range h.cm.All() {
		job := h.syncJobs.Start(name, conn.Type())
		go func(n string, c connector.Connector, jobID string) {
			ctx := context.Background()
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			_, err := h.pipeline.RunWithProgress(ctx, c, progress)
			h.syncJobs.Complete(jobID, err)
			if err != nil {
				h.log.Error("reindex: connector failed", zap.String("connector", n), zap.Error(err))
			}
		}(name, conn, job.ID)
		count++
	}

	h.log.Info("reindex started", zap.Int("dimension", dim), zap.Int("connectors", count))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":    "reindex started",
		"dimension":  dim,
		"connectors": count,
	})
}

func (h *handler) rerankResults(ctx context.Context, query string, result *model.SearchResult) *model.SearchResult {
	reranker := h.rm.Get()
	if reranker == nil || len(result.Documents) <= 1 {
		return result
	}

	texts := make([]string, len(result.Documents))
	for i, doc := range result.Documents {
		texts[i] = doc.Title + " " + doc.Content
	}

	ranked, err := reranker.Rerank(ctx, query, texts)
	if err != nil {
		h.log.Warn("reranking failed, using original order", zap.Error(err))
		return result
	}

	return reorderByRerankScores(result, ranked)
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
