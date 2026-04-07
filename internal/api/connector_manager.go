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
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// ScheduleObserver is notified when connector schedules change.
type ScheduleObserver interface {
	OnConnectorChanged(cfg *model.ConnectorConfig)
	OnConnectorRemoved(id uuid.UUID, name string)
}

// ConnectorManager manages the lifecycle of connectors, bridging
// database-persisted configurations with in-memory connector instances.
type ConnectorManager struct {
	mu            sync.RWMutex
	connectors    map[string]connector.Connector
	store         *store.Store
	log           *zap.Logger
	schedObserver ScheduleObserver
}

// SetScheduleObserver sets the observer that is notified when connector schedules change.
func (m *ConnectorManager) SetScheduleObserver(obs ScheduleObserver) {
	m.schedObserver = obs
}

// NewConnectorManager creates a new ConnectorManager.
func NewConnectorManager(st *store.Store, log *zap.Logger) *ConnectorManager {
	return &ConnectorManager{
		connectors: make(map[string]connector.Connector),
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
		m.connectors[cfg.Name] = conn
		m.log.Info("loaded connector", zap.String("name", cfg.Name), zap.String("type", cfg.Type))
	}

	return nil
}

// Get returns a connector by name.
func (m *ConnectorManager) Get(name string) (connector.Connector, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, ok := m.connectors[name]
	return conn, ok
}

// All returns a snapshot of all active connectors.
func (m *ConnectorManager) All() map[string]connector.Connector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]connector.Connector, len(m.connectors))
	for k, v := range m.connectors {
		result[k] = v
	}
	return result
}

// Add validates a connector config, saves it to the database, and adds it to the in-memory map.
func (m *ConnectorManager) Add(ctx context.Context, cfg *model.ConnectorConfig) error {
	conn, err := m.instantiateConnector(*cfg)
	if err != nil {
		return err
	}

	if err := m.store.CreateConnectorConfig(ctx, cfg); err != nil {
		return err
	}

	if cfg.Enabled {
		m.mu.Lock()
		m.connectors[cfg.Name] = conn
		m.mu.Unlock()
	}

	if m.schedObserver != nil {
		m.schedObserver.OnConnectorChanged(cfg)
	}

	return nil
}

// Update validates a connector config, updates it in the database, and refreshes the in-memory map.
func (m *ConnectorManager) Update(ctx context.Context, cfg *model.ConnectorConfig) error {
	// Get old config to handle name changes
	old, err := m.store.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		return err
	}

	conn, err := m.instantiateConnector(*cfg)
	if err != nil {
		return err
	}

	if err := m.store.UpdateConnectorConfig(ctx, cfg); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove old name if renamed
	if old.Name != cfg.Name {
		delete(m.connectors, old.Name)
	}

	if cfg.Enabled {
		m.connectors[cfg.Name] = conn
	} else {
		delete(m.connectors, cfg.Name)
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

	// Clean up sync cursor
	if err := m.store.DeleteSyncCursor(ctx, cfg.Name); err != nil {
		m.log.Warn("failed to delete sync cursor", zap.String("name", cfg.Name), zap.Error(err))
	}

	m.mu.Lock()
	delete(m.connectors, cfg.Name)
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

	return conn, nil
}
