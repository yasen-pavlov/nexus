//	@title			Nexus API
//	@version		1.0
//	@description	Personal search and RAG tool — indexes data from multiple sources and provides unified search.
//	@host			localhost:8080
//	@BasePath		/api
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer " followed by the JWT returned from /auth/login.

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/muty/nexus/internal/api"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/config"
	_ "github.com/muty/nexus/internal/connector/filesystem" // register filesystem connector via init()
	_ "github.com/muty/nexus/internal/connector/imap"       // register imap connector via init()
	_ "github.com/muty/nexus/internal/connector/paperless"  // register paperless connector via init()
	_ "github.com/muty/nexus/internal/connector/telegram"   // register telegram connector via init()
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/scheduler"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/syncruns"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := newLogger(cfg.LogLevel)
	defer log.Sync() //nolint:errcheck // best-effort flush

	st, err := store.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer st.Close()

	if err := st.RunMigrations(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Post-migration, pre-scheduler: mark any sync_runs still sitting at
	// status='running' as interrupted. These are leftovers from a crash
	// or a force-kill during a previous process — leaving them running
	// would confuse the Activity timeline (and, once the FE starts
	// polling, produce zombie "live" jobs that never terminate).
	if n, err := st.MarkInterruptedStuckRuns(ctx); err != nil {
		log.Warn("mark interrupted stuck sync runs", zap.Error(err))
	} else if n > 0 {
		log.Info("swept interrupted sync runs on startup", zap.Int64("count", n))
	}

	if err := setupEncryption(ctx, st, cfg.EncryptionKey, log); err != nil {
		return err
	}

	em, searchClient, extractorRegistry, binaryStore, err := setupSearchStack(ctx, cfg, st, log)
	if err != nil {
		return err
	}
	go binaryStore.RunEviction(ctx, storage.DefaultCacheConfig, time.Hour)

	cm, p, syncJobs, sched, sweeper, err := setupSyncStack(ctx, cfg, st, em, searchClient, extractorRegistry, binaryStore, log)
	if err != nil {
		return err
	}
	defer sweeper.Stop()

	// Set up reranking
	rm := api.NewRerankManager(st, log)
	if err := rm.LoadFromDB(ctx, cfg); err != nil {
		log.Warn("failed to load rerank settings", zap.Error(err))
	}

	// Ranking config (per-source half-life, floor, trust weight, plus
	// rerank min score + feature toggles). Overlays any persisted overrides
	// on top of the compiled-in defaults.
	rankingMgr := api.NewRankingManager(st, log)
	if err := rankingMgr.LoadFromDB(ctx); err != nil {
		log.Warn("failed to load ranking settings", zap.Error(err))
	}

	jwtSecret, err := resolveJWTSecret(cfg.JWTSecret, log)
	if err != nil {
		return err
	}

	revocationCache, loginLimiter := setupAuthCaches(st)

	router := api.NewRouter(st, searchClient, p, cm, em, rm, syncJobs, binaryStore, sweeper, rankingMgr, jwtSecret, revocationCache, loginLimiter, cfg.CORSOrigins, log)

	return serve(ctx, cfg.Port, router, sched, log)
}

// newLogger builds the zap logger matching the configured log level.
func newLogger(level string) *zap.Logger {
	var log *zap.Logger
	if level == "debug" {
		log, _ = zap.NewDevelopment()
	} else {
		log, _ = zap.NewProduction()
	}
	return log
}

// setupEncryption installs the connector-secret encryption key and migrates
// any still-plaintext configs/settings in-place. No-op when encryption is off.
func setupEncryption(ctx context.Context, st *store.Store, encryptionKey string, log *zap.Logger) error {
	if encryptionKey == "" {
		return nil
	}
	key, err := crypto.NewKey(encryptionKey)
	if err != nil {
		return fmt.Errorf("encryption key: %w", err)
	}
	st.SetEncryptionKey(key)

	n, err := st.EncryptExistingConfigs(ctx)
	if err != nil {
		return fmt.Errorf("encrypt existing configs: %w", err)
	}
	if n > 0 {
		log.Info("encrypted existing connector configs", zap.Int("count", n))
	}

	ns, err := st.EncryptExistingSettings(ctx)
	if err != nil {
		return fmt.Errorf("encrypt existing settings: %w", err)
	}
	if ns > 0 {
		log.Info("encrypted existing sensitive settings", zap.Int("count", ns))
	}
	return nil
}

// setupSearchStack wires the embedding manager, OpenSearch client, extractor
// registry, and binary cache store. OpenSearch mapping-drift is reported as a
// warning so operators know to run POST /api/reindex but the server still
// comes up on stale mappings.
func setupSearchStack(ctx context.Context, cfg *config.Config, st *store.Store, log *zap.Logger) (*api.EmbeddingManager, *search.Client, *extractor.Registry, *storage.BinaryStore, error) {
	em := api.NewEmbeddingManager(st, log)
	if err := em.LoadFromDB(ctx, cfg); err != nil {
		log.Warn("failed to load embedding settings, falling back to env vars", zap.Error(err))
	}

	embeddingDim := em.Dimension()
	if embeddingDim > 0 {
		log.Info("embedding enabled", zap.Int("dimension", embeddingDim))
	} else {
		log.Info("embedding disabled, using BM25-only search")
	}

	// Canonical list of languages Nexus analyzes. Drives both OpenSearch
	// per-field analyzers and the Tika OCR language header. When the
	// Settings UI lands this becomes a DB-backed setting.
	languages := lang.Default()

	searchClient, err := search.New(ctx, cfg.OpenSearchURL, log, languages)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("init search: %w", err)
	}
	if err := searchClient.EnsureIndex(ctx, embeddingDim); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("ensure search index: %w", err)
	}
	// Warn if a pre-existing index is missing the language sub-fields
	// (upgrade path). Non-fatal: multi_match uses lenient:true so queries
	// still work, just without the new stemming. The warning points the
	// admin at /api/reindex to rebuild with the full mapping.
	if ok, err := searchClient.CheckMappingCurrent(ctx); err == nil && !ok {
		log.Warn("search index mapping is out of date; run POST /api/reindex to rebuild with per-language analyzers")
	}

	extractorRegistry := extractor.NewRegistry(cfg.TikaURL, languages)

	binaryStore, err := storage.New(cfg.BinaryStorePath, st, log)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("init binary store: %w", err)
	}
	return em, searchClient, extractorRegistry, binaryStore, nil
}

// setupSyncStack builds the connector manager (seeded from DB + env), the
// pipeline, the sync-job manager, the scheduler, and the retention sweeper.
// The scheduler is started before returning so cron-triggered runs flow
// through the sync-job manager identically to manual triggers.
func setupSyncStack(ctx context.Context, cfg *config.Config, st *store.Store, em *api.EmbeddingManager, searchClient *search.Client, extractorRegistry *extractor.Registry, binaryStore *storage.BinaryStore, log *zap.Logger) (*api.ConnectorManager, *pipeline.Pipeline, *api.SyncJobManager, *scheduler.Scheduler, *syncruns.Sweeper, error) {
	cm := api.NewConnectorManager(st, log)
	cm.SetExtractor(extractorRegistry)
	cm.SetBinaryStore(binaryStore)

	if err := cm.LoadFromDB(ctx); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("load connectors from db: %w", err)
	}
	if err := cm.SeedFromEnv(ctx, cfg); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("seed connectors from env: %w", err)
	}

	// Set up pipeline + sync-job manager. Both get wired into the
	// scheduler before Start() so cron-triggered runs flow through the
	// same lifecycle as manual triggers (sync_runs persistence + SSE).
	p := pipeline.New(st, searchClient, em, log)
	p.SetBinaryStore(binaryStore)
	syncJobs := api.NewSyncJobManager(st, log)

	sched := scheduler.New(cm, p, st, log)
	sched.SetJobManager(syncJobs)
	cm.SetScheduleObserver(sched)

	if err := sched.Start(ctx); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("start scheduler: %w", err)
	}

	// Retention sweeper: periodic cleanup of sync_runs history per the
	// admin settings (defaults: 90-day cutoff, 200 rows/connector, 60min
	// interval). Reads settings on every tick, so the admin UI can
	// retune retention without a restart.
	sweeper := syncruns.NewSweeper(st, st, log)
	sweeper.Start(ctx)

	return cm, p, syncJobs, sched, sweeper, nil
}

// resolveJWTSecret returns the configured JWT secret or generates a random
// one, warning that sessions won't survive restarts in the latter case.
func resolveJWTSecret(configured string, log *zap.Logger) ([]byte, error) {
	if configured != "" {
		return []byte(configured), nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate jwt secret: %w", err)
	}
	log.Warn("no NEXUS_JWT_SECRET set, generated random secret (sessions will not survive restarts)")
	return secret, nil
}

// setupAuthCaches builds the token-revocation cache (30s TTL, looks up
// token_version per user) and the login rate limiter (5 fails / 5min).
func setupAuthCaches(st *store.Store) (*auth.TokenRevocationCache, *auth.LoginRateLimiter) {
	revocationCache := auth.NewTokenRevocationCache(
		func(ctx context.Context, id uuid.UUID) (int, error) {
			u, err := st.GetUserByID(ctx, id)
			if err != nil {
				return 0, err
			}
			return u.TokenVersion, nil
		},
		30*time.Second,
	)
	loginLimiter := auth.NewLoginRateLimiter(auth.DefaultLoginRateLimiterConfig())
	return revocationCache, loginLimiter
}

// serve runs the HTTP server with a graceful shutdown wired to ctx. The
// scheduler is stopped alongside the server so in-flight jobs get a chance
// to finish within the shutdown deadline.
func serve(ctx context.Context, port int, router http.Handler, sched *scheduler.Scheduler, log *zap.Logger) error {
	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		sched.Stop()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("server starting", zap.String("addr", addr))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
