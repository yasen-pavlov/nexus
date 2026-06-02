package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// Settings keys persisted in the settings table for the RAG runtime knobs.
// Phase 5 adds rag_max_tool_rounds; further knobs (max_evidence_chunks,
// max_images_per_turn, etc.) can land here without changing the
// orchestrator wiring.
const (
	ragKeyMaxToolRounds = "rag_max_tool_rounds"
)

// Default + clamp bounds for MaxToolRounds. 0 = "tools disabled — orchestrator
// passes Tools=nil on round 1, forcing a single-shot answer". 5 is a soft
// upper bound: more rounds usually means the model is thrashing on
// paraphrases of the same gap, not making progress.
const (
	defaultMaxToolRounds = 3
	maxAllowedToolRounds = 5
)

// RAGManager owns the runtime knobs the rag.Orchestrator reads per turn,
// with hot-reload via UpdateFromSettings(). Mirrors LLMManager — the
// orchestrator polls MaxToolRounds() once per turn so admin saves take
// effect without restart.
type RAGManager struct {
	mu            sync.RWMutex
	maxToolRounds int

	store *store.Store
	log   *zap.Logger
}

// NewRAGManager constructs the manager with compiled-in defaults; call
// LoadFromDB to overlay persisted settings.
func NewRAGManager(st *store.Store, log *zap.Logger) *RAGManager {
	return &RAGManager{
		store:         st,
		log:           log,
		maxToolRounds: defaultMaxToolRounds,
	}
}

// MaxToolRounds returns the current cap on agentic tool rounds for one
// orchestrator turn. 0 disables tool calls entirely.
func (m *RAGManager) MaxToolRounds() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxToolRounds
}

// RAGSnapshot is the wire shape exposed to the admin GET handler and the
// input to UpdateFromSettings.
type RAGSnapshot struct {
	MaxToolRounds int
}

// Snapshot returns a copy of the current settings.
func (m *RAGManager) Snapshot() RAGSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return RAGSnapshot{MaxToolRounds: m.maxToolRounds}
}

// LoadFromDB reads the persisted RAG settings and overlays the compiled-in
// defaults. Falls back to the env-config-derived default when the DB has
// nothing for a key.
func (m *RAGManager) LoadFromDB(ctx context.Context, _ *config.Config) error {
	settings, err := m.store.GetSettings(ctx, []string{ragKeyMaxToolRounds})
	if err != nil {
		return err
	}
	rounds := defaultMaxToolRounds
	if raw, ok := settings[ragKeyMaxToolRounds]; ok && raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 && v <= maxAllowedToolRounds {
			rounds = v
		} else {
			m.log.Warn("rag: ignoring invalid persisted max_tool_rounds; falling back to default",
				zap.String("raw", raw), zap.Int("default", defaultMaxToolRounds))
		}
	}
	m.mu.Lock()
	m.maxToolRounds = rounds
	m.mu.Unlock()
	return nil
}

// UpdateFromSettings validates new settings, persists them, and hot-swaps
// the live snapshot. Returns a typed error when validation fails so the
// HTTP handler can map it to 400.
func (m *RAGManager) UpdateFromSettings(ctx context.Context, snap RAGSnapshot) error {
	if snap.MaxToolRounds < 0 || snap.MaxToolRounds > maxAllowedToolRounds {
		return fmt.Errorf("max_tool_rounds must be between 0 and %d", maxAllowedToolRounds)
	}
	if err := m.store.SetSettings(ctx, map[string]string{
		ragKeyMaxToolRounds: strconv.Itoa(snap.MaxToolRounds),
	}); err != nil {
		return err
	}
	m.mu.Lock()
	m.maxToolRounds = snap.MaxToolRounds
	m.mu.Unlock()
	m.log.Info("rag settings updated", zap.Int("max_tool_rounds", snap.MaxToolRounds))
	return nil
}

// ErrRAGManagerNotConfigured is returned by handlers when no manager is
// wired (e.g. test wiring). Exposed so test helpers can `errors.Is` it.
var ErrRAGManagerNotConfigured = errors.New("rag manager not configured")
