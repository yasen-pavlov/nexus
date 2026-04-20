// Package syncruns provides the retention sweeper that periodically
// trims the sync_runs history table. Config is sourced from the
// `settings` table at each tick so changes from the admin UI take
// effect within one sweep cycle without requiring a restart.
package syncruns

import (
	"context"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Settings keys read from the store on each tick.
const (
	SettingRetentionDays     = "sync_runs_retention_days"
	SettingRetentionPerConn  = "sync_runs_retention_per_connector"
	SettingSweepIntervalMins = "sync_runs_sweep_interval_minutes"

	DefaultRetentionDays     = 90
	DefaultRetentionPerConn  = 200
	DefaultSweepIntervalMins = 60
)

// MinSweepIntervalMins is the lower bound enforced on the sweep
// interval so a fat-fingered admin can't pin the loop at sub-second
// ticks. Kept as a var (not a const) so tests can drop it to 0 and
// drive the real goroutine with short tickers.
var MinSweepIntervalMins = 5

// intervalUnit is the time unit each config "minute" represents. In
// production this is time.Minute; tests flip it to time.Millisecond so
// the loop can tick in real time without a 5-minute wait.
var intervalUnit = time.Minute

// Config is the resolved retention policy for a single sweep cycle.
type Config struct {
	// RetentionDays: delete terminal runs whose started_at is older than
	// this many days. 0 disables the age-based cutoff.
	RetentionDays int
	// RetentionPerConnector: keep at most N terminal runs per connector
	// regardless of age. 0 disables the per-connector cap.
	RetentionPerConnector int
	// SweepIntervalMinutes: how often the background loop wakes up.
	// Clamped to [MinSweepIntervalMins, ...].
	SweepIntervalMinutes int
}

// SettingsReader is the subset of *store.Store the sweeper needs.
type SettingsReader interface {
	GetSettings(ctx context.Context, keys []string) (map[string]string, error)
}

// RunTrimmer is the subset of *store.Store the sweeper calls when
// pruning. Defined narrow for testability.
type RunTrimmer interface {
	DeleteSyncRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	TrimSyncRunsPerConnector(ctx context.Context, keep int) (int64, error)
}

// LoadConfig reads retention settings from the store and fills missing
// values with defaults. Errors parsing individual keys fall back to
// the default for that key (logged by the caller).
func LoadConfig(ctx context.Context, r SettingsReader) (Config, error) {
	cfg := Config{
		RetentionDays:         DefaultRetentionDays,
		RetentionPerConnector: DefaultRetentionPerConn,
		SweepIntervalMinutes:  DefaultSweepIntervalMins,
	}
	settings, err := r.GetSettings(ctx, []string{
		SettingRetentionDays, SettingRetentionPerConn, SettingSweepIntervalMins,
	})
	if err != nil {
		return cfg, err
	}
	applyIntSetting(settings, SettingRetentionDays, &cfg.RetentionDays, 0)
	applyIntSetting(settings, SettingRetentionPerConn, &cfg.RetentionPerConnector, 0)
	applyIntSetting(settings, SettingSweepIntervalMins, &cfg.SweepIntervalMinutes, 1)

	if cfg.SweepIntervalMinutes < MinSweepIntervalMins {
		cfg.SweepIntervalMinutes = MinSweepIntervalMins
	}
	return cfg, nil
}

// applyIntSetting parses an integer setting into dest when present, non-empty,
// and at least minValue. Silently leaves dest unchanged on any parse failure
// so callers keep their pre-populated defaults.
func applyIntSetting(settings map[string]string, key string, dest *int, minValue int) {
	v, ok := settings[key]
	if !ok || v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minValue {
		return
	}
	*dest = n
}

// Sweeper periodically prunes sync_runs according to retention config.
// Start() spawns a goroutine that reads config on every tick; Stop()
// cancels the goroutine. Safe to call Start once.
type Sweeper struct {
	settings SettingsReader
	trimmer  RunTrimmer
	log      *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSweeper constructs a sweeper. Both deps are typically *store.Store
// in production; narrow interfaces keep the tests cheap.
func NewSweeper(settings SettingsReader, trimmer RunTrimmer, log *zap.Logger) *Sweeper {
	if log == nil {
		log = zap.NewNop()
	}
	return &Sweeper{settings: settings, trimmer: trimmer, log: log}
}

// Start runs the sweep loop in the background. The first sweep fires
// after one interval — if the user wants immediate cleanup on boot,
// they can call SweepOnce directly. Subsequent ticks re-read config so
// the admin can change retention without a restart.
func (s *Sweeper) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done

	go s.loop(runCtx, done)
}

// Stop cancels the loop and blocks until the goroutine exits. Safe to
// call before Start (no-op) or multiple times.
func (s *Sweeper) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// SweepOnce runs the retention rules a single time using the current
// settings. Exposed for tests and for the admin UI's "run now" button
// (to wire later).
func (s *Sweeper) SweepOnce(ctx context.Context) error {
	cfg, err := LoadConfig(ctx, s.settings)
	if err != nil {
		s.log.Warn("sync_runs sweep: load config", zap.Error(err))
		// Fall through with defaults so a store glitch doesn't pause retention entirely.
	}

	if cfg.RetentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
		n, err := s.trimmer.DeleteSyncRunsOlderThan(ctx, cutoff)
		if err != nil {
			return err
		}
		if n > 0 {
			s.log.Info("sync_runs sweep: deleted old rows",
				zap.Int64("deleted", n),
				zap.Int("retention_days", cfg.RetentionDays))
		}
	}

	if cfg.RetentionPerConnector > 0 {
		n, err := s.trimmer.TrimSyncRunsPerConnector(ctx, cfg.RetentionPerConnector)
		if err != nil {
			return err
		}
		if n > 0 {
			s.log.Info("sync_runs sweep: trimmed per-connector excess",
				zap.Int64("deleted", n),
				zap.Int("keep_per_connector", cfg.RetentionPerConnector))
		}
	}
	return nil
}

func (s *Sweeper) loop(ctx context.Context, done chan struct{}) {
	// done is captured as an argument so Stop can nil out s.done without
	// races — the defer below writes to the channel we were spawned with,
	// not whatever s.done happens to be pointing at when the loop exits.
	defer close(done)

	// Re-read config each tick so admin changes apply within one cycle.
	// Use the current interval to schedule the next tick — updating the
	// ticker requires a Stop+NewTicker, which we do inside the loop.
	interval := s.currentInterval(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SweepOnce(ctx); err != nil {
				s.log.Warn("sync_runs sweep: failed", zap.Error(err))
			}
			next := s.currentInterval(ctx)
			if next != interval {
				ticker.Reset(next)
				interval = next
				s.log.Info("sync_runs sweep: interval updated", zap.Duration("interval", interval))
			}
		}
	}
}

func (s *Sweeper) currentInterval(ctx context.Context) time.Duration {
	cfg, err := LoadConfig(ctx, s.settings)
	if err != nil {
		return time.Duration(DefaultSweepIntervalMins) * intervalUnit
	}
	return time.Duration(cfg.SweepIntervalMinutes) * intervalUnit
}
