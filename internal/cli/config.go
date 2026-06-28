// Package cli implements the nexus-cli command tree (Cobra) over the Nexus HTTP
// API. Command bodies stay thin — they resolve config, call internal/cliclient,
// and format output — so the testable logic lives in the client and in the pure
// helpers here.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// envToken overrides any stored token. It is also how the MCP subcommand and
	// CI receive credentials without touching the config file.
	envToken = "NEXUS_TOKEN"
	// envURL overrides the stored / default server URL.
	envURL = "NEXUS_URL"
	// defaultServerURL is the local homelab default.
	defaultServerURL = "http://localhost:8080"
)

// Config is the persisted CLI state, written 0600 under the user config dir.
// Token is a long-lived personal access token (nexus_pat_...); TokenID is its
// server-side row id, kept so a future `logout --revoke` can delete it.
type Config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token,omitempty"`
	TokenID   string `json:"token_id,omitempty"`
	Username  string `json:"username,omitempty"`
}

// configPath returns the credentials file path:
// <user-config-dir>/nexus/credentials.json. os.UserConfigDir is XDG-aware on
// Linux ($XDG_CONFIG_HOME, default ~/.config) and uses
// ~/Library/Application Support on macOS.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "nexus", "credentials.json"), nil
}

// LoadConfig reads the persisted config. A missing file is not an error — it
// returns a zero Config so first-run commands can still resolve env/defaults.
func LoadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// SaveConfig writes cfg as 0600 (dir 0700) so the token is not world-readable.
func SaveConfig(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	// WriteFile only applies the mode when creating the file; enforce 0600 on a
	// pre-existing (possibly looser-permissioned) file too.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	return nil
}

// ClearConfig removes the persisted credentials file. A missing file is a no-op.
func ClearConfig() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove config: %w", err)
	}
	return nil
}

// resolveServerURL applies precedence: flag > NEXUS_URL > stored > default,
// normalizing the chosen value so a bare host still works.
func resolveServerURL(flagVal string, cfg *Config) string {
	switch {
	case flagVal != "":
		return normalizeServerURL(flagVal)
	case os.Getenv(envURL) != "":
		return normalizeServerURL(os.Getenv(envURL))
	case cfg != nil && cfg.ServerURL != "":
		return normalizeServerURL(cfg.ServerURL)
	default:
		return defaultServerURL
	}
}

// normalizeServerURL trims a trailing slash and supplies a scheme when the user
// gave a bare host (e.g. "nexus.bitnet.me"): https:// for a remote host, http://
// for loopback (the dev default). A value that already has a scheme is left
// as-is apart from the trailing-slash trim.
func normalizeServerURL(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" || strings.Contains(s, "://") {
		return s
	}
	host := s
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "http://" + s
	}
	return "https://" + s
}

// resolveToken applies precedence: NEXUS_TOKEN env > OS keychain > 0600 file.
// An empty result means unauthenticated — the caller decides whether that is an
// error. server scopes the keychain lookup so per-server credentials don't mix.
func resolveToken(cfg *Config, server string) string {
	if v := os.Getenv(envToken); v != "" {
		return v
	}
	if tok, ok := loadToken(server); ok {
		return tok
	}
	// File fallback is server-scoped, mirroring the keychain: a token saved for
	// one server must not be sent to a different --server/NEXUS_URL host.
	if cfg != nil && cfg.Token != "" && sameServer(cfg.ServerURL, server) {
		return cfg.Token
	}
	return ""
}

// sameServer compares two server URLs ignoring a trailing slash.
func sameServer(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// persistCredentials saves cfg's metadata plus the secret token, preferring the
// OS keychain and falling back to the 0600 file when the keychain is
// unavailable. When the keychain holds the token, it is kept out of the file.
// It returns a human-readable description of where the token landed.
func persistCredentials(cfg *Config, token string) (where string, err error) {
	if storeToken(cfg.ServerURL, token) {
		cfg.Token = "" // keep the secret out of the file when the keychain has it
		where = "the system keychain"
	} else {
		// Falling back to the file: clear any stale keychain entry for this
		// server so an old keychain token can't shadow the newer file token.
		removeToken(cfg.ServerURL)
		cfg.Token = token
		path, _ := configPath()
		where = path + " (0600)"
	}
	if err := SaveConfig(cfg); err != nil {
		return "", err
	}
	return where, nil
}
