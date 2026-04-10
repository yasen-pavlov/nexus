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

	"github.com/muty/nexus/internal/api"
	"github.com/muty/nexus/internal/config"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	_ "github.com/muty/nexus/internal/connector/imap"
	_ "github.com/muty/nexus/internal/connector/paperless"
	_ "github.com/muty/nexus/internal/connector/telegram"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/scheduler"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
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

	var log *zap.Logger
	if cfg.LogLevel == "debug" {
		log, _ = zap.NewDevelopment()
	} else {
		log, _ = zap.NewProduction()
	}
	defer log.Sync() //nolint:errcheck // best-effort flush

	st, err := store.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer st.Close()

	if err := st.RunMigrations(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Set up encryption for connector secrets
	if cfg.EncryptionKey != "" {
		key, err := crypto.NewKey(cfg.EncryptionKey)
		if err != nil {
			return fmt.Errorf("encryption key: %w", err)
		}
		st.SetEncryptionKey(key)

		// Encrypt any existing plaintext configs
		n, err := st.EncryptExistingConfigs(ctx)
		if err != nil {
			return fmt.Errorf("encrypt existing configs: %w", err)
		}
		if n > 0 {
			log.Info("encrypted existing connector configs", zap.Int("count", n))
		}

		// Encrypt any existing plaintext sensitive settings (API keys, telegram sessions)
		ns, err := st.EncryptExistingSettings(ctx)
		if err != nil {
			return fmt.Errorf("encrypt existing settings: %w", err)
		}
		if ns > 0 {
			log.Info("encrypted existing sensitive settings", zap.Int("count", ns))
		}
	}

	// Set up embedding (DB settings override env vars)
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

	// Set up OpenSearch
	searchClient, err := search.New(ctx, cfg.OpenSearchURL, log)
	if err != nil {
		return fmt.Errorf("init search: %w", err)
	}
	if err := searchClient.EnsureIndex(ctx, embeddingDim); err != nil {
		return fmt.Errorf("ensure search index: %w", err)
	}

	// Set up content extraction
	extractorRegistry := extractor.NewRegistry(cfg.TikaURL)

	// Set up connector manager
	cm := api.NewConnectorManager(st, log)
	cm.SetExtractor(extractorRegistry)

	if err := cm.LoadFromDB(ctx); err != nil {
		return fmt.Errorf("load connectors from db: %w", err)
	}

	if err := cm.SeedFromEnv(ctx, cfg); err != nil {
		return fmt.Errorf("seed connectors from env: %w", err)
	}

	// Set up scheduler
	p := pipeline.New(st, searchClient, em, log)
	sched := scheduler.New(cm, p, st, log)
	cm.SetScheduleObserver(sched)

	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	// Set up reranking
	rm := api.NewRerankManager(st, log)
	if err := rm.LoadFromDB(ctx, cfg); err != nil {
		log.Warn("failed to load rerank settings", zap.Error(err))
	}

	// Set up JWT secret
	jwtSecret := []byte(cfg.JWTSecret)
	if len(jwtSecret) == 0 {
		jwtSecret = make([]byte, 32)
		if _, err := rand.Read(jwtSecret); err != nil {
			return fmt.Errorf("generate jwt secret: %w", err)
		}
		log.Warn("no NEXUS_JWT_SECRET set, generated random secret (sessions will not survive restarts)")
	}

	syncJobs := api.NewSyncJobManager()
	router := api.NewRouter(st, searchClient, p, cm, em, rm, syncJobs, jwtSecret, cfg.CORSOrigins, log)

	addr := fmt.Sprintf(":%d", cfg.Port)
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
