package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("NEXUS_DATABASE_URL", "postgres://test:test@localhost/test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log level 'info', got %q", cfg.LogLevel)
	}
	if cfg.FSPatterns != "*.txt,*.md" {
		t.Errorf("expected default patterns '*.txt,*.md', got %q", cfg.FSPatterns)
	}
	if cfg.DatabaseURL != "postgres://test:test@localhost/test" {
		t.Errorf("unexpected database URL: %q", cfg.DatabaseURL)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("NEXUS_DATABASE_URL", "postgres://custom@localhost/nexus")
	t.Setenv("NEXUS_PORT", "9090")
	t.Setenv("NEXUS_LOG_LEVEL", "debug")
	t.Setenv("NEXUS_FS_ROOT_PATH", "/data/files")
	t.Setenv("NEXUS_FS_PATTERNS", "*.txt,*.md,*.log")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %q", cfg.LogLevel)
	}
	if cfg.FSRootPath != "/data/files" {
		t.Errorf("expected FS root '/data/files', got %q", cfg.FSRootPath)
	}
	if cfg.FSPatterns != "*.txt,*.md,*.log" {
		t.Errorf("expected patterns '*.txt,*.md,*.log', got %q", cfg.FSPatterns)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	// envconfig requires the var to be completely absent, not just empty
	os.Unsetenv("NEXUS_DATABASE_URL") //nolint:errcheck // test setup

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}
