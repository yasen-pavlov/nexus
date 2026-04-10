package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

type handler struct {
	store     *store.Store
	search    *search.Client
	pipeline  *pipeline.Pipeline
	em        *EmbeddingManager
	rm        *RerankManager
	cm        *ConnectorManager
	syncJobs  *SyncJobManager
	jwtSecret []byte
	log       *zap.Logger
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
		OwnerID:     auth.UserIDFromContext(r.Context()).String(),
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

	// Initialize score details if explain mode is on
	explain := r.URL.Query().Get("score_details") == "true"
	if explain {
		for i := range result.Documents {
			result.Documents[i].ScoreDetails = &model.ScoreDetails{
				Retrieval: result.Documents[i].Rank,
			}
		}
	}

	// Rerank results if a reranker is available
	result = h.rerankResults(r.Context(), query, result)

	if explain {
		for i := range result.Documents {
			if result.Documents[i].ScoreDetails != nil {
				result.Documents[i].ScoreDetails.Reranker = result.Documents[i].Rank
			}
		}
	}

	// Apply recency decay — boost recent documents, source-specific half-lives
	search.ApplyRecencyDecay(result)

	// Apply metadata bonus — boost results matching query terms in structured metadata
	search.ApplyMetadataBonus(result, query)

	writeJSON(w, http.StatusOK, result)
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
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}
	conn, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}

	if !canModifyConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}

	// Check if a sync is already running for this connector
	if existing := h.syncJobs.GetByConnector(cfg.ID); existing != nil {
		writeError(w, http.StatusConflict, "sync already running for "+cfg.Name)
		return
	}

	job := h.syncJobs.Start(cfg.ID, cfg.Name, conn.Type())
	snapshot := *job // copy before goroutine can mutate it

	// Run pipeline in background with a detached context
	go func() {
		ctx := context.Background()
		progress := func(total, processed, errors int) {
			h.syncJobs.Update(job.ID, total, processed, errors)
		}

		ownerID := ""
		if cfg.UserID != nil {
			ownerID = cfg.UserID.String()
		}
		_, err := h.pipeline.RunWithProgress(ctx, cfg.ID, conn, ownerID, cfg.Shared, progress)
		h.syncJobs.Complete(job.ID, err)

		if err != nil {
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
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}

	_, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}

	if !canReadConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, "connector not found")
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
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}
	_, cfg, ok := h.cm.GetByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	if !canModifyConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, "connector not found")
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
		if existing := h.syncJobs.GetByConnector(connID); existing != nil {
			continue // already running
		}
		connName := entry.Conn.Name()
		ownerID := ""
		if entry.Config.UserID != nil {
			ownerID = entry.Config.UserID.String()
		}
		job := h.syncJobs.Start(connID, connName, entry.Conn.Type())
		snapshot := *job
		jobs = append(jobs, &snapshot)

		go func(cid uuid.UUID, n string, c connector.Connector, oid string, shared bool, jobID string) {
			ctx := context.Background()
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			_, err := h.pipeline.RunWithProgress(ctx, cid, c, oid, shared, progress)
			h.syncJobs.Complete(jobID, err)
			if err != nil {
				h.log.Error("sync all: connector failed", zap.String("connector", n), zap.Error(err))
			}
		}(connID, connName, entry.Conn, ownerID, entry.Config.Shared, job.ID)
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
		connName := entry.Conn.Name()
		ownerID := ""
		if entry.Config.UserID != nil {
			ownerID = entry.Config.UserID.String()
		}
		job := h.syncJobs.Start(connID, connName, entry.Conn.Type())
		go func(cid uuid.UUID, n string, c connector.Connector, oid string, shared bool, jobID string) {
			ctx := context.Background()
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			_, err := h.pipeline.RunWithProgress(ctx, cid, c, oid, shared, progress)
			h.syncJobs.Complete(jobID, err)
			if err != nil {
				h.log.Error("reindex: connector failed", zap.String("connector", n), zap.Error(err))
			}
		}(connID, connName, entry.Conn, ownerID, entry.Config.Shared, job.ID)
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
