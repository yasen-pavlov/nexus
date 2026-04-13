// Package api provides the HTTP handlers and routing for the Nexus REST API.
package api

import (
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"

	_ "github.com/muty/nexus/docs"
)

func NewRouter(
	store *store.Store,
	search *search.Client,
	pipeline *pipeline.Pipeline,
	cm *ConnectorManager,
	em *EmbeddingManager,
	rm *RerankManager,
	syncJobs *SyncJobManager,
	binaryStore *storage.BinaryStore,
	jwtSecret []byte,
	corsOrigins []string,
	log *zap.Logger,
) chi.Router {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(10 * time.Minute))
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"http://localhost:5173"}
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	h := &handler{
		store:       store,
		search:      search,
		pipeline:    pipeline,
		em:          em,
		rm:          rm,
		cm:          cm,
		syncJobs:    syncJobs,
		binaryStore: binaryStore,
		jwtSecret:   jwtSecret,
		log:         log,
	}

	// SSE endpoint — outside the timeout middleware (long-lived connection)
	// Protected by auth
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(jwtSecret))
		r.Get("/api/sync/{id}/progress", h.StreamSyncProgress)
	})

	r.Route("/api", func(r chi.Router) {
		// Public routes (no auth required)
		r.Get("/health", h.Health)
		r.Post("/auth/register", h.Register)
		r.Post("/auth/login", h.Login)

		// Protected routes (any authenticated user)
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(jwtSecret))

			r.Get("/auth/me", h.Me)
			r.Get("/search", h.Search)

			r.Get("/documents/{id}/content", h.DownloadDocument)
			r.Get("/documents/{id}/related", h.GetRelatedDocuments)
			r.Get("/conversations/{source_type}/{conversation_id}/messages", h.GetConversationMessages)

			r.Get("/sync", h.ListSyncJobs)
			r.Post("/sync", h.SyncAll)
			r.Post("/sync/{id}", h.TriggerSync)
			r.Delete("/sync/cursors/{id}", h.DeleteCursor)

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

			// Password change (admin or self)
			r.Put("/users/{id}/password", h.ChangePassword)

			// Admin-only routes
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))

				r.Post("/reindex", h.TriggerReindex)
				r.Delete("/sync/cursors", h.DeleteAllCursors)
				r.Get("/storage/stats", h.GetStorageStats)
				r.Delete("/storage/cache", h.DeleteStorageCache)
				r.Delete("/storage/cache/{id}", h.DeleteStorageCacheByConnector)

				r.Route("/settings", func(r chi.Router) {
					r.Get("/embedding", h.GetEmbeddingSettings)
					r.Put("/embedding", h.UpdateEmbeddingSettings)
					r.Get("/rerank", h.GetRerankSettings)
					r.Put("/rerank", h.UpdateRerankSettings)
				})

				r.Post("/users", h.CreateUser)
				r.Get("/users", h.ListUsers)
				r.Delete("/users/{id}", h.DeleteUser)
			})
		})
	})

	// Swagger UI (public)
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// Serve static frontend files
	staticHandler := staticFileHandler()
	if staticHandler != nil {
		r.Handle("/*", staticHandler)
	}

	return r
}
