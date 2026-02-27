package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromConfigFile(t *testing.T) {
	clearConfigEnvs(t)

	configPath := writeTempConfig(t, `{
		"http_addr": "127.0.0.1:18080",
		"log_level": "debug",
		"database_dsn": "postgres://user:pass@localhost:5432/noovertime?sslmode=disable",
		"db_pool_max_conns": 20,
		"db_pool_min_conns": 2,
		"db_pool_max_lifetime_sec": 7200,
		"db_pool_max_idle_sec": 600
	}`)
	t.Setenv("CONFIG_FILE", configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:18080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.DatabaseDSN != "postgres://user:pass@localhost:5432/noovertime?sslmode=disable" {
		t.Fatalf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}
	if cfg.DBPoolMaxConns != 20 || cfg.DBPoolMinConns != 2 {
		t.Fatalf("pool conns = %d/%d", cfg.DBPoolMaxConns, cfg.DBPoolMinConns)
	}
	if cfg.DBPoolMaxLifetimeSec != 7200 || cfg.DBPoolMaxIdleTimeSec != 600 {
		t.Fatalf("pool lifetime/idle = %d/%d", cfg.DBPoolMaxLifetimeSec, cfg.DBPoolMaxIdleTimeSec)
	}
}

func TestLoadEnvOverridesConfigFile(t *testing.T) {
	clearConfigEnvs(t)

	configPath := writeTempConfig(t, `{
		"http_addr": "127.0.0.1:18080",
		"log_level": "info",
		"database_dsn": "postgres://file:file@localhost:5432/noovertime?sslmode=disable",
		"db_pool_max_conns": 10,
		"db_pool_min_conns": 1,
		"db_pool_max_lifetime_sec": 3600,
		"db_pool_max_idle_sec": 300
	}`)
	t.Setenv("CONFIG_FILE", configPath)
	t.Setenv("HTTP_ADDR", "127.0.0.1:28080")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("DATABASE_DSN", "postgres://env:env@localhost:5432/noovertime?sslmode=disable")
	t.Setenv("DB_POOL_MAX_CONNS", "25")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:28080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.DatabaseDSN != "postgres://env:env@localhost:5432/noovertime?sslmode=disable" {
		t.Fatalf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}
	if cfg.DBPoolMaxConns != 25 {
		t.Fatalf("DBPoolMaxConns = %d", cfg.DBPoolMaxConns)
	}
}

func TestLoadMissingDatabaseDSN(t *testing.T) {
	clearConfigEnvs(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_DSN is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadInvalidConfigFilePath(t *testing.T) {
	clearConfigEnvs(t)

	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.json"))

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read CONFIG_FILE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnknownFieldInConfigFile(t *testing.T) {
	clearConfigEnvs(t)

	configPath := writeTempConfig(t, `{
		"database_dsn": "postgres://user:pass@localhost:5432/noovertime?sslmode=disable",
		"unexpected_key": "x"
	}`)
	t.Setenv("CONFIG_FILE", configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse CONFIG_FILE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsInvalidIntEnv(t *testing.T) {
	clearConfigEnvs(t)

	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/noovertime?sslmode=disable")
	t.Setenv("DB_POOL_MAX_CONNS", "abc")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "DB_POOL_MAX_CONNS must be an integer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func clearConfigEnvs(t *testing.T) {
	t.Helper()
	keys := []string{
		"CONFIG_FILE",
		"HTTP_ADDR",
		"LOG_LEVEL",
		"DATABASE_DSN",
		"DB_POOL_MAX_CONNS",
		"DB_POOL_MIN_CONNS",
		"DB_POOL_MAX_LIFETIME_SEC",
		"DB_POOL_MAX_IDLE_SEC",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}
