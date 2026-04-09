// Package api provides the HTTP handlers and routing for the Nexus REST API.
package api

import (
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

func NewRouter(
	store *store.Store,
	search *search.Client,
	pipeline *pipeline.Pipeline,
	cm *ConnectorManager,
	em *EmbeddingManager,
	syncJobs *SyncJobManager,
	log *zap.Logger,
) chi.Router {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(10 * time.Minute))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	h := &handler{
		store:    store,
		search:   search,
		pipeline: pipeline,
		em:       em,
		cm:       cm,
		syncJobs: syncJobs,
		log:      log,
	}

	// SSE endpoint — outside the timeout middleware (long-lived connection)
	r.Get("/api/sync/{connector}/progress", h.StreamSyncProgress)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", h.Health)
		r.Get("/search", h.Search)
		r.Get("/sync", h.ListSyncJobs)
		r.Post("/sync", h.SyncAll)
		r.Post("/sync/{connector}", h.TriggerSync)
		r.Delete("/sync/cursors", h.DeleteAllCursors)
		r.Delete("/sync/cursors/{connector}", h.DeleteCursor)
		r.Post("/reindex", h.TriggerReindex)

		r.Route("/connectors", func(r chi.Router) {
			r.Get("/", h.ListConnectors)
			r.Post("/", h.CreateConnector)
			r.Get("/{id}", h.GetConnector)
			r.Put("/{id}", h.UpdateConnector)
			r.Delete("/{id}", h.DeleteConnector)
		})

		r.Route("/connectors/{id}/auth", func(r chi.Router) {
			r.Post("/start", h.TelegramAuthStart)
			r.Post("/code", h.TelegramAuthCode)
		})

		r.Route("/settings", func(r chi.Router) {
			r.Get("/embedding", h.GetEmbeddingSettings)
			r.Put("/embedding", h.UpdateEmbeddingSettings)
		})
	})

	// Serve static frontend files
	staticHandler := staticFileHandler()
	if staticHandler != nil {
		r.Handle("/*", staticHandler)
	}

	return r
}
