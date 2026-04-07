package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muty/nexus/internal/api"
	"github.com/muty/nexus/internal/config"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	_ "github.com/muty/nexus/internal/connector/paperless"
	"github.com/muty/nexus/internal/pipeline"
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

	// Set up OpenSearch
	searchClient, err := search.New(ctx, cfg.OpenSearchURL, log)
	if err != nil {
		return fmt.Errorf("init search: %w", err)
	}
	if err := searchClient.EnsureIndex(ctx); err != nil {
		return fmt.Errorf("ensure search index: %w", err)
	}

	// Set up connector manager
	cm := api.NewConnectorManager(st, log)

	if err := cm.LoadFromDB(ctx); err != nil {
		return fmt.Errorf("load connectors from db: %w", err)
	}

	if err := cm.SeedFromEnv(ctx, cfg); err != nil {
		return fmt.Errorf("seed connectors from env: %w", err)
	}

	// Set up scheduler
	p := pipeline.New(st, searchClient, log)
	sched := scheduler.New(cm, p, st, log)
	cm.SetScheduleObserver(sched)

	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	router := api.NewRouter(st, searchClient, p, cm, log)

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
