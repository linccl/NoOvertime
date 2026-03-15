package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	defaultHTTPAddr             = ":29082"
	defaultLogLevel             = "info"
	defaultDBPoolMaxConns       = 10
	defaultDBPoolMinConns       = 1
	defaultDBPoolMaxLifetimeSec = 3600
	defaultDBPoolMaxIdleSec     = 300
	defaultUploadStorageBackend = "local"
	defaultUploadLocalDir       = "./uploads-data"
)

type UploadStoreConfig struct {
	StorageBackend     string
	LocalDir           string
	PublicBaseURL      string
	OSSEndpoint        string
	OSSBucket          string
	OSSAccessKeyID     string
	OSSAccessKeySecret string
	OSSPrefix          string
}

// Config is the minimal application configuration for bootstrap.
type Config struct {
	HTTPAddr                           string
	LogLevel                           string
	DatabaseDSN                        string
	DBPoolMaxConns                     int
	DBPoolMinConns                     int
	DBPoolMaxLifetimeSec               int
	DBPoolMaxIdleTimeSec               int
	UploadStorageBackend               string
	UploadLocalDir                     string
	UploadPublicBaseURL                string
	UploadOSSEndpoint                  string
	UploadOSSBucket                    string
	UploadOSSAccessKeyID               string
	UploadOSSAccessKeySecret           string
	UploadOSSPrefix                    string
	PunchPhotoUploadStorageBackend     string
	PunchPhotoUploadLocalDir           string
	PunchPhotoUploadPublicBaseURL      string
	PunchPhotoUploadOSSEndpoint        string
	PunchPhotoUploadOSSBucket          string
	PunchPhotoUploadOSSAccessKeyID     string
	PunchPhotoUploadOSSAccessKeySecret string
	PunchPhotoUploadOSSPrefix          string
	LogUploadStorageBackend            string
	LogUploadLocalDir                  string
	LogUploadPublicBaseURL             string
	LogUploadOSSEndpoint               string
	LogUploadOSSBucket                 string
	LogUploadOSSAccessKeyID            string
	LogUploadOSSAccessKeySecret        string
	LogUploadOSSPrefix                 string
}

type fileConfig struct {
	HTTPAddr                           *string `json:"http_addr"`
	LogLevel                           *string `json:"log_level"`
	DatabaseDSN                        *string `json:"database_dsn"`
	DBPoolMaxConns                     *int    `json:"db_pool_max_conns"`
	DBPoolMinConns                     *int    `json:"db_pool_min_conns"`
	DBPoolMaxLifetimeSec               *int    `json:"db_pool_max_lifetime_sec"`
	DBPoolMaxIdleSec                   *int    `json:"db_pool_max_idle_sec"`
	UploadStorageBackend               *string `json:"upload_storage_backend"`
	UploadLocalDir                     *string `json:"upload_local_dir"`
	UploadPublicBaseURL                *string `json:"upload_public_base_url"`
	UploadOSSEndpoint                  *string `json:"upload_oss_endpoint"`
	UploadOSSBucket                    *string `json:"upload_oss_bucket"`
	UploadOSSAccessKeyID               *string `json:"upload_oss_access_key_id"`
	UploadOSSAccessKeySecret           *string `json:"upload_oss_access_key_secret"`
	UploadOSSPrefix                    *string `json:"upload_oss_prefix"`
	PunchPhotoUploadStorageBackend     *string `json:"punch_photo_upload_storage_backend"`
	PunchPhotoUploadLocalDir           *string `json:"punch_photo_upload_local_dir"`
	PunchPhotoUploadPublicBaseURL      *string `json:"punch_photo_upload_public_base_url"`
	PunchPhotoUploadOSSEndpoint        *string `json:"punch_photo_upload_oss_endpoint"`
	PunchPhotoUploadOSSBucket          *string `json:"punch_photo_upload_oss_bucket"`
	PunchPhotoUploadOSSAccessKeyID     *string `json:"punch_photo_upload_oss_access_key_id"`
	PunchPhotoUploadOSSAccessKeySecret *string `json:"punch_photo_upload_oss_access_key_secret"`
	PunchPhotoUploadOSSPrefix          *string `json:"punch_photo_upload_oss_prefix"`
	LogUploadStorageBackend            *string `json:"log_upload_storage_backend"`
	LogUploadLocalDir                  *string `json:"log_upload_local_dir"`
	LogUploadPublicBaseURL             *string `json:"log_upload_public_base_url"`
	LogUploadOSSEndpoint               *string `json:"log_upload_oss_endpoint"`
	LogUploadOSSBucket                 *string `json:"log_upload_oss_bucket"`
	LogUploadOSSAccessKeyID            *string `json:"log_upload_oss_access_key_id"`
	LogUploadOSSAccessKeySecret        *string `json:"log_upload_oss_access_key_secret"`
	LogUploadOSSPrefix                 *string `json:"log_upload_oss_prefix"`
}

// Load reads configuration from config file first, then environment variables.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:             defaultHTTPAddr,
		LogLevel:             defaultLogLevel,
		DBPoolMaxConns:       defaultDBPoolMaxConns,
		DBPoolMinConns:       defaultDBPoolMinConns,
		DBPoolMaxLifetimeSec: defaultDBPoolMaxLifetimeSec,
		DBPoolMaxIdleTimeSec: defaultDBPoolMaxIdleSec,
		UploadStorageBackend: defaultUploadStorageBackend,
		UploadLocalDir:       defaultUploadLocalDir,
	}

	if configPath, ok := getNonEmptyEnv("CONFIG_FILE"); ok {
		fileCfg, err := loadConfigFile(configPath)
		if err != nil {
			return Config{}, err
		}
		applyFileConfig(&cfg, fileCfg)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadConfigFile(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("read CONFIG_FILE %q: %w", path, err)
	}
	defer file.Close()

	var cfg fileConfig
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse CONFIG_FILE %q: %w", path, err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fileConfig{}, fmt.Errorf("parse CONFIG_FILE %q: unexpected trailing content", path)
	}

	return cfg, nil
}

func applyFileConfig(cfg *Config, raw fileConfig) {
	if raw.HTTPAddr != nil {
		cfg.HTTPAddr = strings.TrimSpace(*raw.HTTPAddr)
	}
	if raw.LogLevel != nil {
		cfg.LogLevel = strings.ToLower(strings.TrimSpace(*raw.LogLevel))
	}
	if raw.DatabaseDSN != nil {
		cfg.DatabaseDSN = strings.TrimSpace(*raw.DatabaseDSN)
	}
	if raw.DBPoolMaxConns != nil {
		cfg.DBPoolMaxConns = *raw.DBPoolMaxConns
	}
	if raw.DBPoolMinConns != nil {
		cfg.DBPoolMinConns = *raw.DBPoolMinConns
	}
	if raw.DBPoolMaxLifetimeSec != nil {
		cfg.DBPoolMaxLifetimeSec = *raw.DBPoolMaxLifetimeSec
	}
	if raw.DBPoolMaxIdleSec != nil {
		cfg.DBPoolMaxIdleTimeSec = *raw.DBPoolMaxIdleSec
	}
	if raw.UploadStorageBackend != nil {
		cfg.UploadStorageBackend = strings.ToLower(strings.TrimSpace(*raw.UploadStorageBackend))
	}
	if raw.UploadLocalDir != nil {
		cfg.UploadLocalDir = strings.TrimSpace(*raw.UploadLocalDir)
	}
	if raw.UploadPublicBaseURL != nil {
		cfg.UploadPublicBaseURL = strings.TrimSpace(*raw.UploadPublicBaseURL)
	}
	if raw.UploadOSSEndpoint != nil {
		cfg.UploadOSSEndpoint = strings.TrimSpace(*raw.UploadOSSEndpoint)
	}
	if raw.UploadOSSBucket != nil {
		cfg.UploadOSSBucket = strings.TrimSpace(*raw.UploadOSSBucket)
	}
	if raw.UploadOSSAccessKeyID != nil {
		cfg.UploadOSSAccessKeyID = strings.TrimSpace(*raw.UploadOSSAccessKeyID)
	}
	if raw.UploadOSSAccessKeySecret != nil {
		cfg.UploadOSSAccessKeySecret = strings.TrimSpace(*raw.UploadOSSAccessKeySecret)
	}
	if raw.UploadOSSPrefix != nil {
		cfg.UploadOSSPrefix = strings.TrimSpace(*raw.UploadOSSPrefix)
	}
	if raw.PunchPhotoUploadStorageBackend != nil {
		cfg.PunchPhotoUploadStorageBackend = strings.ToLower(strings.TrimSpace(*raw.PunchPhotoUploadStorageBackend))
	}
	if raw.PunchPhotoUploadLocalDir != nil {
		cfg.PunchPhotoUploadLocalDir = strings.TrimSpace(*raw.PunchPhotoUploadLocalDir)
	}
	if raw.PunchPhotoUploadPublicBaseURL != nil {
		cfg.PunchPhotoUploadPublicBaseURL = strings.TrimSpace(*raw.PunchPhotoUploadPublicBaseURL)
	}
	if raw.PunchPhotoUploadOSSEndpoint != nil {
		cfg.PunchPhotoUploadOSSEndpoint = strings.TrimSpace(*raw.PunchPhotoUploadOSSEndpoint)
	}
	if raw.PunchPhotoUploadOSSBucket != nil {
		cfg.PunchPhotoUploadOSSBucket = strings.TrimSpace(*raw.PunchPhotoUploadOSSBucket)
	}
	if raw.PunchPhotoUploadOSSAccessKeyID != nil {
		cfg.PunchPhotoUploadOSSAccessKeyID = strings.TrimSpace(*raw.PunchPhotoUploadOSSAccessKeyID)
	}
	if raw.PunchPhotoUploadOSSAccessKeySecret != nil {
		cfg.PunchPhotoUploadOSSAccessKeySecret = strings.TrimSpace(*raw.PunchPhotoUploadOSSAccessKeySecret)
	}
	if raw.PunchPhotoUploadOSSPrefix != nil {
		cfg.PunchPhotoUploadOSSPrefix = strings.TrimSpace(*raw.PunchPhotoUploadOSSPrefix)
	}
	if raw.LogUploadStorageBackend != nil {
		cfg.LogUploadStorageBackend = strings.ToLower(strings.TrimSpace(*raw.LogUploadStorageBackend))
	}
	if raw.LogUploadLocalDir != nil {
		cfg.LogUploadLocalDir = strings.TrimSpace(*raw.LogUploadLocalDir)
	}
	if raw.LogUploadPublicBaseURL != nil {
		cfg.LogUploadPublicBaseURL = strings.TrimSpace(*raw.LogUploadPublicBaseURL)
	}
	if raw.LogUploadOSSEndpoint != nil {
		cfg.LogUploadOSSEndpoint = strings.TrimSpace(*raw.LogUploadOSSEndpoint)
	}
	if raw.LogUploadOSSBucket != nil {
		cfg.LogUploadOSSBucket = strings.TrimSpace(*raw.LogUploadOSSBucket)
	}
	if raw.LogUploadOSSAccessKeyID != nil {
		cfg.LogUploadOSSAccessKeyID = strings.TrimSpace(*raw.LogUploadOSSAccessKeyID)
	}
	if raw.LogUploadOSSAccessKeySecret != nil {
		cfg.LogUploadOSSAccessKeySecret = strings.TrimSpace(*raw.LogUploadOSSAccessKeySecret)
	}
	if raw.LogUploadOSSPrefix != nil {
		cfg.LogUploadOSSPrefix = strings.TrimSpace(*raw.LogUploadOSSPrefix)
	}
}

func applyEnvOverrides(cfg *Config) error {
	if value, ok := getNonEmptyEnv("HTTP_ADDR"); ok {
		cfg.HTTPAddr = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_LEVEL"); ok {
		cfg.LogLevel = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := getNonEmptyEnv("DATABASE_DSN"); ok {
		cfg.DatabaseDSN = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_STORAGE_BACKEND"); ok {
		cfg.UploadStorageBackend = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := getNonEmptyEnv("UPLOAD_LOCAL_DIR"); ok {
		cfg.UploadLocalDir = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_PUBLIC_BASE_URL"); ok {
		cfg.UploadPublicBaseURL = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_OSS_ENDPOINT"); ok {
		cfg.UploadOSSEndpoint = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_OSS_BUCKET"); ok {
		cfg.UploadOSSBucket = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_OSS_ACCESS_KEY_ID"); ok {
		cfg.UploadOSSAccessKeyID = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_OSS_ACCESS_KEY_SECRET"); ok {
		cfg.UploadOSSAccessKeySecret = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("UPLOAD_OSS_PREFIX"); ok {
		cfg.UploadOSSPrefix = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_STORAGE_BACKEND"); ok {
		cfg.PunchPhotoUploadStorageBackend = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_LOCAL_DIR"); ok {
		cfg.PunchPhotoUploadLocalDir = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_PUBLIC_BASE_URL"); ok {
		cfg.PunchPhotoUploadPublicBaseURL = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_OSS_ENDPOINT"); ok {
		cfg.PunchPhotoUploadOSSEndpoint = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_OSS_BUCKET"); ok {
		cfg.PunchPhotoUploadOSSBucket = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_OSS_ACCESS_KEY_ID"); ok {
		cfg.PunchPhotoUploadOSSAccessKeyID = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_OSS_ACCESS_KEY_SECRET"); ok {
		cfg.PunchPhotoUploadOSSAccessKeySecret = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("PUNCH_PHOTO_UPLOAD_OSS_PREFIX"); ok {
		cfg.PunchPhotoUploadOSSPrefix = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_STORAGE_BACKEND"); ok {
		cfg.LogUploadStorageBackend = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_LOCAL_DIR"); ok {
		cfg.LogUploadLocalDir = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_PUBLIC_BASE_URL"); ok {
		cfg.LogUploadPublicBaseURL = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_OSS_ENDPOINT"); ok {
		cfg.LogUploadOSSEndpoint = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_OSS_BUCKET"); ok {
		cfg.LogUploadOSSBucket = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_OSS_ACCESS_KEY_ID"); ok {
		cfg.LogUploadOSSAccessKeyID = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_OSS_ACCESS_KEY_SECRET"); ok {
		cfg.LogUploadOSSAccessKeySecret = strings.TrimSpace(value)
	}
	if value, ok := getNonEmptyEnv("LOG_UPLOAD_OSS_PREFIX"); ok {
		cfg.LogUploadOSSPrefix = strings.TrimSpace(value)
	}

	if value, ok, err := getOptionalIntEnv("DB_POOL_MAX_CONNS"); err != nil {
		return err
	} else if ok {
		cfg.DBPoolMaxConns = value
	}
	if value, ok, err := getOptionalIntEnv("DB_POOL_MIN_CONNS"); err != nil {
		return err
	} else if ok {
		cfg.DBPoolMinConns = value
	}
	if value, ok, err := getOptionalIntEnv("DB_POOL_MAX_LIFETIME_SEC"); err != nil {
		return err
	} else if ok {
		cfg.DBPoolMaxLifetimeSec = value
	}
	if value, ok, err := getOptionalIntEnv("DB_POOL_MAX_IDLE_SEC"); err != nil {
		return err
	} else if ok {
		cfg.DBPoolMaxIdleTimeSec = value
	}

	return nil
}

func validate(cfg Config) error {
	if cfg.HTTPAddr == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if err := validateHTTPAddr(cfg.HTTPAddr); err != nil {
		return err
	}

	if cfg.DatabaseDSN == "" {
		return fmt.Errorf("DATABASE_DSN is required")
	}

	if !isValidLogLevel(cfg.LogLevel) {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}

	if cfg.DBPoolMaxConns <= 0 {
		return fmt.Errorf("DB_POOL_MAX_CONNS must be > 0")
	}

	if cfg.DBPoolMinConns < 0 {
		return fmt.Errorf("DB_POOL_MIN_CONNS must be >= 0")
	}
	if cfg.DBPoolMinConns > cfg.DBPoolMaxConns {
		return fmt.Errorf("DB_POOL_MIN_CONNS must be <= DB_POOL_MAX_CONNS")
	}

	if cfg.DBPoolMaxLifetimeSec <= 0 {
		return fmt.Errorf("DB_POOL_MAX_LIFETIME_SEC must be > 0")
	}

	if cfg.DBPoolMaxIdleTimeSec <= 0 {
		return fmt.Errorf("DB_POOL_MAX_IDLE_SEC must be > 0")
	}
	if cfg.DBPoolMaxIdleTimeSec > cfg.DBPoolMaxLifetimeSec {
		return fmt.Errorf("DB_POOL_MAX_IDLE_SEC must be <= DB_POOL_MAX_LIFETIME_SEC")
	}

	if err := validateUploadStoreConfig("UPLOAD", cfg.DefaultUploadStoreConfig()); err != nil {
		return err
	}
	if err := validateUploadStoreConfig("PUNCH_PHOTO_UPLOAD", cfg.PunchPhotoUploadStoreConfig()); err != nil {
		return err
	}
	if err := validateUploadStoreConfig("LOG_UPLOAD", cfg.LogUploadStoreConfig()); err != nil {
		return err
	}

	return nil
}

func (cfg Config) DefaultUploadStoreConfig() UploadStoreConfig {
	return UploadStoreConfig{
		StorageBackend:     cfg.UploadStorageBackend,
		LocalDir:           cfg.UploadLocalDir,
		PublicBaseURL:      cfg.UploadPublicBaseURL,
		OSSEndpoint:        cfg.UploadOSSEndpoint,
		OSSBucket:          cfg.UploadOSSBucket,
		OSSAccessKeyID:     cfg.UploadOSSAccessKeyID,
		OSSAccessKeySecret: cfg.UploadOSSAccessKeySecret,
		OSSPrefix:          cfg.UploadOSSPrefix,
	}
}

func (cfg Config) PunchPhotoUploadStoreConfig() UploadStoreConfig {
	return mergeUploadStoreConfig(cfg.DefaultUploadStoreConfig(), UploadStoreConfig{
		StorageBackend:     cfg.PunchPhotoUploadStorageBackend,
		LocalDir:           cfg.PunchPhotoUploadLocalDir,
		PublicBaseURL:      cfg.PunchPhotoUploadPublicBaseURL,
		OSSEndpoint:        cfg.PunchPhotoUploadOSSEndpoint,
		OSSBucket:          cfg.PunchPhotoUploadOSSBucket,
		OSSAccessKeyID:     cfg.PunchPhotoUploadOSSAccessKeyID,
		OSSAccessKeySecret: cfg.PunchPhotoUploadOSSAccessKeySecret,
		OSSPrefix:          cfg.PunchPhotoUploadOSSPrefix,
	})
}

func (cfg Config) LogUploadStoreConfig() UploadStoreConfig {
	return mergeUploadStoreConfig(cfg.DefaultUploadStoreConfig(), UploadStoreConfig{
		StorageBackend:     cfg.LogUploadStorageBackend,
		LocalDir:           cfg.LogUploadLocalDir,
		PublicBaseURL:      cfg.LogUploadPublicBaseURL,
		OSSEndpoint:        cfg.LogUploadOSSEndpoint,
		OSSBucket:          cfg.LogUploadOSSBucket,
		OSSAccessKeyID:     cfg.LogUploadOSSAccessKeyID,
		OSSAccessKeySecret: cfg.LogUploadOSSAccessKeySecret,
		OSSPrefix:          cfg.LogUploadOSSPrefix,
	})
}

func mergeUploadStoreConfig(base, override UploadStoreConfig) UploadStoreConfig {
	if value := strings.ToLower(strings.TrimSpace(override.StorageBackend)); value != "" {
		base.StorageBackend = value
	}
	if value := strings.TrimSpace(override.LocalDir); value != "" {
		base.LocalDir = value
	}
	if value := strings.TrimSpace(override.PublicBaseURL); value != "" {
		base.PublicBaseURL = value
	}
	if value := strings.TrimSpace(override.OSSEndpoint); value != "" {
		base.OSSEndpoint = value
	}
	if value := strings.TrimSpace(override.OSSBucket); value != "" {
		base.OSSBucket = value
	}
	if value := strings.TrimSpace(override.OSSAccessKeyID); value != "" {
		base.OSSAccessKeyID = value
	}
	if value := strings.TrimSpace(override.OSSAccessKeySecret); value != "" {
		base.OSSAccessKeySecret = value
	}
	if value := strings.TrimSpace(override.OSSPrefix); value != "" {
		base.OSSPrefix = value
	}
	return base
}

func validateUploadStoreConfig(prefix string, cfg UploadStoreConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.StorageBackend)) {
	case "local":
		if strings.TrimSpace(cfg.LocalDir) == "" {
			return fmt.Errorf("%s_LOCAL_DIR is required when %s_STORAGE_BACKEND=local", prefix, prefix)
		}
	case "oss":
		if strings.TrimSpace(cfg.OSSEndpoint) == "" {
			return fmt.Errorf("%s_OSS_ENDPOINT is required when %s_STORAGE_BACKEND=oss", prefix, prefix)
		}
		if strings.TrimSpace(cfg.OSSBucket) == "" {
			return fmt.Errorf("%s_OSS_BUCKET is required when %s_STORAGE_BACKEND=oss", prefix, prefix)
		}
		if strings.TrimSpace(cfg.OSSAccessKeyID) == "" || strings.TrimSpace(cfg.OSSAccessKeySecret) == "" {
			return fmt.Errorf("%s_OSS_ACCESS_KEY_ID and %s_OSS_ACCESS_KEY_SECRET are required when %s_STORAGE_BACKEND=oss", prefix, prefix, prefix)
		}
	default:
		return fmt.Errorf("%s_STORAGE_BACKEND must be one of: local, oss", prefix)
	}

	return nil
}

func getNonEmptyEnv(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	return value, true
}

func getOptionalIntEnv(key string) (int, bool, error) {
	raw, ok := getNonEmptyEnv(key)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%s must be an integer", key)
	}
	return value, true, nil
}

func isValidLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validateHTTPAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR must be in host:port format")
	}
	if strings.Contains(host, " ") {
		return fmt.Errorf("HTTP_ADDR host must not contain spaces")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("HTTP_ADDR port must be between 1 and 65535")
	}
	return nil
}
