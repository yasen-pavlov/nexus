// Package api provides the HTTP handlers and routing for the Nexus REST API.
package api

import (
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

func NewRouter(
	store *store.Store,
	pipeline *pipeline.Pipeline,
	connectors map[string]connector.Connector,
	log *zap.Logger,
) chi.Router {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	h := &handler{
		store:      store,
		pipeline:   pipeline,
		connectors: connectors,
		log:        log,
	}

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", h.Health)
		r.Get("/search", h.Search)
		r.Post("/sync/{connector}", h.TriggerSync)
		r.Get("/connectors", h.ListConnectors)
	})

	// Serve static frontend files
	staticHandler := staticFileHandler()
	if staticHandler != nil {
		r.Handle("/*", staticHandler)
	}

	return r
}
