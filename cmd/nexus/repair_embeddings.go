package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/muty/nexus/internal/api"
	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
)

func repairEmbeddings(args []string) error {
	flags := flag.NewFlagSet("repair-embeddings", flag.ContinueOnError)
	parent := flags.String("parent-id", "", "exact indexed parent_id to repair")
	snapshot := flags.String("snapshot", "", "new private backup file (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *parent == "" || *snapshot == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: nexus repair-embeddings -parent-id <exact-parent-id> -snapshot <new-file>; stop the server first")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 30*time.Minute)
	defer timeout()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger("debug")
	defer log.Sync() //nolint:errcheck // best-effort cleanup
	st, err := store.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer st.Close()
	if cfg.EncryptionKey != "" {
		key, err := crypto.NewKey(cfg.EncryptionKey)
		if err != nil {
			return err
		}
		st.SetEncryptionKey(key)
	}
	em := api.NewEmbeddingManager(st, log)
	if err := em.LoadFromDB(ctx, cfg); err != nil {
		return err
	}
	if em.Get() == nil {
		return fmt.Errorf("embedding is disabled")
	}
	client, err := search.New(ctx, cfg.OpenSearchURL, log, lang.Default(), search.WithAuth(openSearchAuth(cfg)))
	if err != nil {
		return err
	}
	return client.RepairMissingEmbeddings(ctx, *parent, *snapshot, em.Get(), pipeline.EligibleForEmbedding)
}
