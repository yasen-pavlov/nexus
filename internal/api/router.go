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
	"github.com/muty/nexus/internal/syncruns"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"

	_ "github.com/muty/nexus/docs" // register generated swagger docs with httpSwagger handler
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
	sweeper *syncruns.Sweeper,
	ranking *RankingManager,
	jwtSecret []byte,
	revocation *auth.TokenRevocationCache,
	loginLimiter *auth.LoginRateLimiter,
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
		store:        store,
		search:       search,
		pipeline:     pipeline,
		em:           em,
		rm:           rm,
		cm:           cm,
		syncJobs:     syncJobs,
		binaryStore:  binaryStore,
		sweeper:      sweeper,
		ranking:      ranking,
		jwtSecret:    jwtSecret,
		revocation:   revocation,
		loginLimiter: loginLimiter,
		log:          log,
	}

	// SSE endpoints — outside the timeout middleware (long-lived connections)
	// Protected by auth (Bearer header or ?token= query param for EventSource).
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(jwtSecret))
		r.Use(auth.RevocationMiddleware(revocation))
		// Multiplexed stream: one connection, all visible jobs.
		r.Get("/api/sync/progress", h.StreamAllSyncProgress)
		// Per-job legacy stream, kept for backward compat.
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
			r.Use(auth.RevocationMiddleware(revocation))

			r.Get("/auth/me", h.Me)
			r.Get("/me/identities", h.GetMyIdentities)
			r.Get("/search", h.Search)

			r.Get("/documents/by-source", h.GetDocumentBySource)
			r.Get("/documents/{id}/content", h.DownloadDocument)
			r.Get("/documents/{id}/related", h.GetRelatedDocuments)
			r.Get("/conversations/{source_type}/{conversation_id}/messages", h.GetConversationMessages)

			r.Get("/sync", h.ListSyncJobs)
			r.Post("/sync", h.SyncAll)
			r.Post("/sync/{id}", h.TriggerSync)
			r.Post("/sync/jobs/{id}/cancel", h.CancelSyncJob)
			r.Delete("/sync/cursors/{id}", h.DeleteCursor)

			r.Route("/connectors", func(r chi.Router) {
				r.Get("/", h.ListConnectors)
				r.Post("/", h.CreateConnector)
				r.Get("/{id}", h.GetConnector)
				r.Put("/{id}", h.UpdateConnector)
				r.Delete("/{id}", h.DeleteConnector)
				r.Get("/{id}/avatars/{external_id}", h.GetConnectorAvatar)
				r.Get("/{id}/runs", h.ListSyncRunsForConnector)
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
				r.Get("/admin/stats", h.GetAdminStats)
				r.Get("/storage/stats", h.GetStorageStats)
				r.Delete("/storage/cache", h.DeleteStorageCache)
				r.Delete("/storage/cache/{id}", h.DeleteStorageCacheByConnector)

				r.Route("/settings", func(r chi.Router) {
					r.Get("/embedding", h.GetEmbeddingSettings)
					r.Put("/embedding", h.UpdateEmbeddingSettings)
					r.Get("/rerank", h.GetRerankSettings)
					r.Put("/rerank", h.UpdateRerankSettings)
					r.Get("/retention", h.GetRetentionSettings)
					r.Put("/retention", h.UpdateRetentionSettings)
					r.Post("/retention/sweep", h.RunRetentionSweep)
					r.Get("/ranking", h.GetRankingSettings)
					r.Put("/ranking", h.UpdateRankingSettings)
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
