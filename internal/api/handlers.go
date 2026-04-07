package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

type handler struct {
	store    *store.Store
	search   *search.Client
	pipeline *pipeline.Pipeline
	em       *EmbeddingManager
	cm       *ConnectorManager
	syncJobs *SyncJobManager
	log      *zap.Logger
}

func (h *handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
	embedder := h.em.Get()
	if embedder != nil {
		embeddings, err := embedder.Embed(r.Context(), []string{query})
		if err == nil && len(embeddings) > 0 {
			result, err := h.search.HybridSearch(r.Context(), req, embeddings[0])
			if err == nil {
				writeJSON(w, http.StatusOK, result)
				return
			}
			h.log.Warn("hybrid search failed, falling back to BM25", zap.Error(err))
		}
	}

	// Fallback: BM25-only
	result, err := h.search.Search(r.Context(), req)
	if err != nil {
		h.log.Error("search failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

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

	writeJSON(w, http.StatusAccepted, job)
}

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

func (h *handler) ListSyncJobs(w http.ResponseWriter, _ *http.Request) {
	jobs := h.syncJobs.Active()
	writeJSON(w, http.StatusOK, jobs)
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
