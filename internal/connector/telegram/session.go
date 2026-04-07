// Package telegram implements a connector for Telegram using the MTProto User API.
package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/gotd/td/session"
)

// DBSessionStorage persists Telegram session data using a key-value store.
type DBSessionStorage struct {
	mu         sync.Mutex
	key        string
	getSetting func(ctx context.Context, key string) (string, error)
	setSetting func(ctx context.Context, key, value string) error
	data       []byte
}

// NewDBSessionStorage creates a session storage backed by the settings store.
func NewDBSessionStorage(key string, get func(ctx context.Context, key string) (string, error), set func(ctx context.Context, key, value string) error) *DBSessionStorage {
	return &DBSessionStorage{key: key, getSetting: get, setSetting: set}
}

// LoadSession loads session data from the database.
func (s *DBSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data != nil {
		return s.data, nil
	}

	val, err := s.getSetting(context.Background(), s.key)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, session.ErrNotFound
	}

	data, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		return nil, err
	}
	s.data = data
	return data, nil
}

// StoreSession saves session data to the database.
func (s *DBSessionStorage) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = data
	encoded := base64.StdEncoding.EncodeToString(data)
	return s.setSetting(context.Background(), s.key, encoded)
}

// HasSession checks if a session exists.
func (s *DBSessionStorage) HasSession(ctx context.Context) bool {
	_, err := s.LoadSession(ctx)
	return err == nil
}

// SessionData returns the raw session for JSON serialization (for auth flow).
type SessionData struct {
	Data json.RawMessage `json:"data"`
}
