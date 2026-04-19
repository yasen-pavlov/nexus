package api

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// Settings keys for ranking config persistence. The rerank-min-score key
// pulls double duty: it's moved to the Rerank settings endpoint in the
// UI, but still persisted under this key for backward-compat with rows
// written before the move.
const (
	settingRankSourceHalfLife     = "rank_source_half_life_days"
	settingRankSourceRecencyFloor = "rank_source_recency_floor"
	settingRankSourceTrustWeight  = "rank_source_trust_weight"
	settingRankRerankMinScore     = "rank_reranker_min_score"
	settingRankMetadataBonus      = "rank_metadata_bonus_enabled"
	settingRankSourceTrustEnabled = "rank_source_trust_enabled"
)

// RankingManager holds the active RankingConfig and hot-swaps it when the
// admin saves new values. Thread-safe for concurrent search-path reads —
// Get() returns a shallow copy; map fields share backing storage with the
// cached config, so callers must treat them as read-only.
type RankingManager struct {
	mu    sync.RWMutex
	cfg   search.RankingConfig
	store *store.Store
	log   *zap.Logger
}

// NewRankingManager constructs a manager seeded with the compiled-in
// defaults. Call LoadFromDB to overlay any persisted overrides.
func NewRankingManager(st *store.Store, log *zap.Logger) *RankingManager {
	if log == nil {
		log = zap.NewNop()
	}
	return &RankingManager{
		cfg:   search.DefaultRankingConfig(),
		store: st,
		log:   log,
	}
}

// Get returns the current config snapshot.
func (m *RankingManager) Get() search.RankingConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// LoadFromDB reads persisted values and merges them over the defaults.
// Missing keys (or a corrupt JSON payload on any one key) fall through to
// the defaults — a busted row never takes down the whole manager.
func (m *RankingManager) LoadFromDB(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	keys := []string{
		settingRankSourceHalfLife,
		settingRankSourceRecencyFloor,
		settingRankSourceTrustWeight,
		settingRankRerankMinScore,
		settingRankMetadataBonus,
		settingRankSourceTrustEnabled,
	}
	settings, err := m.store.GetSettings(ctx, keys)
	if err != nil {
		return err
	}
	cfg := search.DefaultRankingConfig()
	if v := settings[settingRankSourceHalfLife]; v != "" {
		overlayFloatMap(cfg.SourceHalfLifeDays, v)
	}
	if v := settings[settingRankSourceRecencyFloor]; v != "" {
		overlayFloatMap(cfg.SourceRecencyFloor, v)
	}
	if v := settings[settingRankSourceTrustWeight]; v != "" {
		overlayFloatMap(cfg.SourceTrustWeight, v)
	}
	if v := settings[settingRankRerankMinScore]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RerankerMinScore = f
		}
	}
	if v := settings[settingRankMetadataBonus]; v != "" {
		cfg.MetadataBonusEnabled = v == "true"
	}
	if v := settings[settingRankSourceTrustEnabled]; v != "" {
		cfg.SourceTrustEnabled = v == "true"
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	m.log.Info("loaded ranking config from db")
	return nil
}

// Replace persists the supplied config + hot-swaps the in-memory copy so
// the next query sees the new values. Caller is responsible for bounds +
// known-source-type validation before calling.
func (m *RankingManager) Replace(ctx context.Context, cfg search.RankingConfig) error {
	halfLifeJSON, _ := json.Marshal(cfg.SourceHalfLifeDays) //nolint:errcheck // map[string]float64 never fails
	recencyJSON, _ := json.Marshal(cfg.SourceRecencyFloor)  //nolint:errcheck
	trustJSON, _ := json.Marshal(cfg.SourceTrustWeight)     //nolint:errcheck

	if err := m.store.SetSettings(ctx, map[string]string{
		settingRankSourceHalfLife:     string(halfLifeJSON),
		settingRankSourceRecencyFloor: string(recencyJSON),
		settingRankSourceTrustWeight:  string(trustJSON),
		settingRankRerankMinScore:     strconv.FormatFloat(cfg.RerankerMinScore, 'f', -1, 64),
		settingRankMetadataBonus:      strconv.FormatBool(cfg.MetadataBonusEnabled),
		settingRankSourceTrustEnabled: strconv.FormatBool(cfg.SourceTrustEnabled),
	}); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	m.log.Info("ranking config replaced")
	return nil
}

// SetRerankerMinScore is a narrow mutator for the Rerank settings
// endpoint, which now owns this single scalar. Keeps the rest of the
// config untouched + hot-swaps in-memory.
func (m *RankingManager) SetRerankerMinScore(ctx context.Context, score float64) error {
	if err := m.store.SetSetting(ctx, settingRankRerankMinScore,
		strconv.FormatFloat(score, 'f', -1, 64)); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg.RerankerMinScore = score
	m.mu.Unlock()
	return nil
}

// overlayFloatMap mutates dst with JSON-encoded overrides. Silent on
// invalid JSON (leaves dst untouched) so a single corrupt setting row
// can't zero out the defaults.
func overlayFloatMap(dst map[string]float64, encoded string) {
	var overlay map[string]float64
	if err := json.Unmarshal([]byte(encoded), &overlay); err != nil {
		return
	}
	for k, v := range overlay {
		dst[k] = v
	}
}
