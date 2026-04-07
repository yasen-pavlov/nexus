package api

import (
	"net/http"
	"strconv"

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
		Query:  query,
		Limit:  limit,
		Offset: offset,
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

	report, err := h.pipeline.Run(r.Context(), conn)
	if err != nil {
		h.log.Error("sync failed", zap.String("connector", name), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "sync failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, report)
}
