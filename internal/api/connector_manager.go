package api

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/connector"
	tgconn "github.com/muty/nexus/internal/connector/telegram"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// ScheduleObserver is notified when connector schedules change.
type ScheduleObserver interface {
	OnConnectorChanged(cfg *model.ConnectorConfig)
	OnConnectorRemoved(id uuid.UUID, name string)
}

// connectorEntry holds a connector instance along with its config metadata.
type connectorEntry struct {
	conn   connector.Connector
	config model.ConnectorConfig
}

// ConnectorManager manages the lifecycle of connectors, bridging
// database-persisted configurations with in-memory connector instances.
type ConnectorManager struct {
	mu            sync.RWMutex
	connectors    map[uuid.UUID]*connectorEntry
	store         *store.Store
	log           *zap.Logger
	schedObserver ScheduleObserver
	extractor     *extractor.Registry
	binaryStore   *storage.BinaryStore
}

// SetScheduleObserver sets the observer that is notified when connector schedules change.
func (m *ConnectorManager) SetScheduleObserver(obs ScheduleObserver) {
	m.schedObserver = obs
}

// SetExtractor sets the content extractor registry for filesystem connectors.
func (m *ConnectorManager) SetExtractor(ext *extractor.Registry) {
	m.extractor = ext
}

// SetBinaryStore sets the binary content cache. Connectors that
// implement connector.CacheAware receive it (and their resolved cache
// policy) during instantiation.
func (m *ConnectorManager) SetBinaryStore(bs *storage.BinaryStore) {
	m.binaryStore = bs
}

// NewConnectorManager creates a new ConnectorManager.
func NewConnectorManager(st *store.Store, log *zap.Logger) *ConnectorManager {
	return &ConnectorManager{
		connectors: make(map[uuid.UUID]*connectorEntry),
		store:      st,
		log:        log,
	}
}

// LoadFromDB loads all enabled connector configs from the database,
// instantiates connectors, and populates the in-memory map.
func (m *ConnectorManager) LoadFromDB(ctx context.Context) error {
	configs, err := m.store.ListConnectorConfigs(ctx)
	if err != nil {
		return fmt.Errorf("connector manager: list configs: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		conn, err := m.instantiateConnector(cfg)
		if err != nil {
			m.log.Warn("failed to load connector",
				zap.String("name", cfg.Name),
				zap.String("type", cfg.Type),
				zap.Error(err),
			)
			continue
		}
		m.connectors[cfg.ID] = &connectorEntry{conn: conn, config: cfg}
		m.log.Info("loaded connector", zap.String("name", cfg.Name), zap.String("type", cfg.Type))
	}

	return nil
}

// GetByID returns a connector by its config UUID.
func (m *ConnectorManager) GetByID(id uuid.UUID) (connector.Connector, *model.ConnectorConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.connectors[id]
	if !ok {
		return nil, nil, false
	}
	cfg := e.config
	return e.conn, &cfg, true
}

// GetByTypeAndName returns the active connector instance matching the given
// (source_type, source_name) pair. Used by the download endpoint to dispatch
// from a stored document back to the connector that produced it.
func (m *ConnectorManager) GetByTypeAndName(typeStr, nameStr string) (connector.Connector, *model.ConnectorConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.connectors {
		if e.config.Type == typeStr && e.config.Name == nameStr {
			cfg := e.config
			return e.conn, &cfg, true
		}
	}
	return nil, nil, false
}

// ConnectorWithConfig pairs a connector instance with its config.
type ConnectorWithConfig struct {
	Conn   connector.Connector
	Config model.ConnectorConfig
}

// All returns all active connectors with their configs.
func (m *ConnectorManager) All() map[uuid.UUID]ConnectorWithConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[uuid.UUID]ConnectorWithConfig, len(m.connectors))
	for id, e := range m.connectors {
		result[id] = ConnectorWithConfig{Conn: e.conn, Config: e.config}
	}
	return result
}

// Add validates a connector config, saves it to the database, and adds it to the in-memory map.
func (m *ConnectorManager) Add(ctx context.Context, cfg *model.ConnectorConfig) error {
	// Assign the UUID up front so connector instantiation (e.g. the telegram
	// session storage key) sees the same ID that the persisted row will have.
	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}

	conn, err := m.instantiateConnector(*cfg)
	if err != nil {
		return err
	}

	if err := m.store.CreateConnectorConfig(ctx, cfg); err != nil {
		return err
	}

	if cfg.Enabled {
		m.mu.Lock()
		m.connectors[cfg.ID] = &connectorEntry{conn: conn, config: *cfg}
		m.mu.Unlock()
	}

	if m.schedObserver != nil {
		m.schedObserver.OnConnectorChanged(cfg)
	}

	return nil
}

// Update validates a connector config, updates it in the database, and refreshes the in-memory map.
func (m *ConnectorManager) Update(ctx context.Context, cfg *model.ConnectorConfig) error {
	conn, err := m.instantiateConnector(*cfg)
	if err != nil {
		return err
	}

	if err := m.store.UpdateConnectorConfig(ctx, cfg); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Enabled {
		m.connectors[cfg.ID] = &connectorEntry{conn: conn, config: *cfg}
	} else {
		delete(m.connectors, cfg.ID)
	}

	if m.schedObserver != nil {
		m.schedObserver.OnConnectorChanged(cfg)
	}

	return nil
}

// Remove deletes a connector config from the database and removes it from the in-memory map.
func (m *ConnectorManager) Remove(ctx context.Context, id uuid.UUID) error {
	cfg, err := m.store.GetConnectorConfig(ctx, id)
	if err != nil {
		return err
	}

	if err := m.store.DeleteConnectorConfig(ctx, id); err != nil {
		return err
	}

	// Clean up sync cursor (the FK ON DELETE CASCADE handles it too, but
	// being explicit avoids relying on schema for correctness).
	if err := m.store.DeleteSyncCursor(ctx, id); err != nil {
		m.log.Warn("failed to delete sync cursor", zap.String("name", cfg.Name), zap.Error(err))
	}

	// Clean up cached binaries for this connector — keyed by
	// (source_type, source_name), matching search.DeleteBySource.
	if m.binaryStore != nil {
		if err := m.binaryStore.DeleteBySource(ctx, cfg.Type, cfg.Name); err != nil {
			m.log.Warn("failed to delete cached binaries",
				zap.String("type", cfg.Type),
				zap.String("name", cfg.Name),
				zap.Error(err),
			)
		}
	}

	m.mu.Lock()
	delete(m.connectors, id)
	m.mu.Unlock()

	if m.schedObserver != nil {
		m.schedObserver.OnConnectorRemoved(id, cfg.Name)
	}

	return nil
}

// SeedFromEnv creates a filesystem connector from env vars if one doesn't already exist in the DB.
func (m *ConnectorManager) SeedFromEnv(ctx context.Context, appCfg *config.Config) error {
	if appCfg.FSRootPath == "" {
		return nil
	}

	configs, err := m.store.ListConnectorConfigs(ctx)
	if err != nil {
		return fmt.Errorf("connector manager: seed: %w", err)
	}

	for _, cfg := range configs {
		if cfg.Name == "filesystem" {
			return nil // already seeded
		}
	}

	cfg := &model.ConnectorConfig{
		Type:    "filesystem",
		Name:    "filesystem",
		Config:  map[string]any{"root_path": appCfg.FSRootPath, "patterns": appCfg.FSPatterns},
		Enabled: true,
		Shared:  true, // filesystem connector is shared by default
	}

	if err := m.Add(ctx, cfg); err != nil {
		return fmt.Errorf("connector manager: seed filesystem: %w", err)
	}

	m.log.Info("seeded filesystem connector from env vars",
		zap.String("root_path", appCfg.FSRootPath),
		zap.String("patterns", appCfg.FSPatterns),
	)
	return nil
}

func (m *ConnectorManager) instantiateConnector(cfg model.ConnectorConfig) (connector.Connector, error) {
	conn, err := connector.Create(cfg.Type)
	if err != nil {
		return nil, fmt.Errorf("unknown connector type %q: %w", cfg.Type, err)
	}

	cfgMap := connector.Config(cfg.Config)
	cfgMap["name"] = cfg.Name

	if err := conn.Configure(cfgMap); err != nil {
		return nil, fmt.Errorf("invalid config for %q: %w", cfg.Name, err)
	}

	if err := conn.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed for %q: %w", cfg.Name, err)
	}

	// Inject extractor for connectors that support it (filesystem, imap, telegram)
	if (cfg.Type == "filesystem" || cfg.Type == "imap" || cfg.Type == "telegram") && m.extractor != nil {
		if extConn, ok := conn.(interface {
			SetExtractor(*extractor.Registry)
		}); ok {
			extConn.SetExtractor(m.extractor)
		}
	}

	// Inject session storage for Telegram connectors
	if cfg.Type == "telegram" {
		if tgConn, ok := conn.(interface {
			SetSession(*tgconn.DBSessionStorage)
		}); ok {
			sessionKey := fmt.Sprintf("telegram_session_%s", cfg.ID.String())
			session := tgconn.NewDBSessionStorage(sessionKey, m.store.GetSetting, m.store.SetSetting)
			tgConn.SetSession(session)
		}
	}

	// Inject binary cache + resolved per-connector policy for
	// connectors that opt in via CacheAware. Connectors that don't
	// implement it (filesystem, paperless) never touch the cache and
	// always re-fetch from source.
	if m.binaryStore != nil {
		if ca, ok := conn.(connector.CacheAware); ok {
			cacheCfg := storage.ResolveCacheConfig(cfg.Type, cfg.Config)
			ca.SetBinaryStore(m.binaryStore, connector.CacheConfig{Mode: string(cacheCfg.Mode)})
		}
	}

	return conn, nil
}
