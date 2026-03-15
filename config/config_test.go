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
		"db_pool_max_idle_sec": 600,
		"upload_storage_backend": "oss",
		"upload_public_base_url": "https://cdn.example.com/noovertime",
		"upload_oss_endpoint": "https://oss-cn-hangzhou.aliyuncs.com",
		"upload_oss_bucket": "noovertime-test",
		"upload_oss_access_key_id": "ak",
		"upload_oss_access_key_secret": "sk",
		"upload_oss_prefix": "prod/mobile"
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
	if cfg.UploadStorageBackend != "oss" {
		t.Fatalf("UploadStorageBackend = %q", cfg.UploadStorageBackend)
	}
	if cfg.UploadPublicBaseURL != "https://cdn.example.com/noovertime" {
		t.Fatalf("UploadPublicBaseURL = %q", cfg.UploadPublicBaseURL)
	}
	if cfg.UploadOSSPrefix != "prod/mobile" {
		t.Fatalf("UploadOSSPrefix = %q", cfg.UploadOSSPrefix)
	}
}

func TestTargetUploadConfigInheritsDefaultAndOverridesBucket(t *testing.T) {
	clearConfigEnvs(t)

	configPath := writeTempConfig(t, `{
		"database_dsn": "postgres://user:pass@localhost:5432/noovertime?sslmode=disable",
		"upload_storage_backend": "oss",
		"upload_public_base_url": "https://cdn.example.com/shared",
		"upload_oss_endpoint": "https://oss-cn-hangzhou.aliyuncs.com",
		"upload_oss_bucket": "shared-bucket",
		"upload_oss_access_key_id": "ak",
		"upload_oss_access_key_secret": "sk",
		"upload_oss_prefix": "shared",
		"punch_photo_upload_oss_bucket": "photo-bucket",
		"punch_photo_upload_oss_prefix": "punch-photos",
		"log_upload_oss_bucket": "log-bucket",
		"log_upload_oss_prefix": "logs"
	}`)
	t.Setenv("CONFIG_FILE", configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	photoCfg := cfg.PunchPhotoUploadStoreConfig()
	if photoCfg.StorageBackend != "oss" {
		t.Fatalf("photo backend = %q", photoCfg.StorageBackend)
	}
	if photoCfg.OSSBucket != "photo-bucket" {
		t.Fatalf("photo bucket = %q", photoCfg.OSSBucket)
	}
	if photoCfg.OSSEndpoint != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("photo endpoint = %q", photoCfg.OSSEndpoint)
	}
	if photoCfg.PublicBaseURL != "https://cdn.example.com/shared" {
		t.Fatalf("photo public base url = %q", photoCfg.PublicBaseURL)
	}

	logCfg := cfg.LogUploadStoreConfig()
	if logCfg.OSSBucket != "log-bucket" {
		t.Fatalf("log bucket = %q", logCfg.OSSBucket)
	}
	if logCfg.OSSPrefix != "logs" {
		t.Fatalf("log prefix = %q", logCfg.OSSPrefix)
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
	t.Setenv("UPLOAD_STORAGE_BACKEND", "local")
	t.Setenv("UPLOAD_LOCAL_DIR", "/tmp/noovertime-uploads")

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
	if cfg.UploadStorageBackend != "local" {
		t.Fatalf("UploadStorageBackend = %q", cfg.UploadStorageBackend)
	}
	if cfg.UploadLocalDir != "/tmp/noovertime-uploads" {
		t.Fatalf("UploadLocalDir = %q", cfg.UploadLocalDir)
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

func TestLoadRejectsIncompleteOSSConfig(t *testing.T) {
	clearConfigEnvs(t)

	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/noovertime?sslmode=disable")
	t.Setenv("UPLOAD_STORAGE_BACKEND", "oss")
	t.Setenv("UPLOAD_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "UPLOAD_OSS_BUCKET is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsIncompletePunchPhotoOSSConfig(t *testing.T) {
	clearConfigEnvs(t)

	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/noovertime?sslmode=disable")
	t.Setenv("PUNCH_PHOTO_UPLOAD_STORAGE_BACKEND", "oss")
	t.Setenv("PUNCH_PHOTO_UPLOAD_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "PUNCH_PHOTO_UPLOAD_OSS_BUCKET is required") {
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
		"UPLOAD_STORAGE_BACKEND",
		"UPLOAD_LOCAL_DIR",
		"UPLOAD_PUBLIC_BASE_URL",
		"UPLOAD_OSS_ENDPOINT",
		"UPLOAD_OSS_BUCKET",
		"UPLOAD_OSS_ACCESS_KEY_ID",
		"UPLOAD_OSS_ACCESS_KEY_SECRET",
		"UPLOAD_OSS_PREFIX",
		"PUNCH_PHOTO_UPLOAD_STORAGE_BACKEND",
		"PUNCH_PHOTO_UPLOAD_LOCAL_DIR",
		"PUNCH_PHOTO_UPLOAD_PUBLIC_BASE_URL",
		"PUNCH_PHOTO_UPLOAD_OSS_ENDPOINT",
		"PUNCH_PHOTO_UPLOAD_OSS_BUCKET",
		"PUNCH_PHOTO_UPLOAD_OSS_ACCESS_KEY_ID",
		"PUNCH_PHOTO_UPLOAD_OSS_ACCESS_KEY_SECRET",
		"PUNCH_PHOTO_UPLOAD_OSS_PREFIX",
		"LOG_UPLOAD_STORAGE_BACKEND",
		"LOG_UPLOAD_LOCAL_DIR",
		"LOG_UPLOAD_PUBLIC_BASE_URL",
		"LOG_UPLOAD_OSS_ENDPOINT",
		"LOG_UPLOAD_OSS_BUCKET",
		"LOG_UPLOAD_OSS_ACCESS_KEY_ID",
		"LOG_UPLOAD_OSS_ACCESS_KEY_SECRET",
		"LOG_UPLOAD_OSS_PREFIX",
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
