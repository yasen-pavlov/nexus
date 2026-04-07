// Package config handles application configuration loaded from environment variables.
package config

import "github.com/kelseyhightower/envconfig"

type Config struct {
	Port          int    `envconfig:"PORT" default:"8080"`
	DatabaseURL   string `envconfig:"DATABASE_URL" required:"true"`
	OpenSearchURL string `envconfig:"OPENSEARCH_URL" default:"http://localhost:9200"`
	LogLevel      string `envconfig:"LOG_LEVEL" default:"info"`

	// Filesystem connector
	FSRootPath string `envconfig:"FS_ROOT_PATH"`
	FSPatterns string `envconfig:"FS_PATTERNS" default:"*.txt,*.md"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("nexus", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
