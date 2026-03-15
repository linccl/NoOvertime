package main

import (
	"context"
	"log"
	"strings"

	"noovertime/config"
	"noovertime/internal/api"
	"noovertime/internal/db"
	"noovertime/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	dbClient := db.NewClient(
		db.NewPoolConfig(
			cfg.DatabaseDSN,
			cfg.DBPoolMaxConns,
			cfg.DBPoolMinConns,
			cfg.DBPoolMaxLifetimeSec,
			cfg.DBPoolMaxIdleTimeSec,
		),
	)

	if err := dbClient.Connect(context.Background()); err != nil {
		log.Fatalf("connect database: %v", err)
	}
	if err := dbClient.Ping(context.Background()); err != nil {
		log.Fatalf("ping database: %v", err)
	}
	defer dbClient.Close()

	punchPhotoStore, punchPhotoLocalDir, err := newUploadStore(cfg.PunchPhotoUploadStoreConfig())
	if err != nil {
		log.Fatalf("init punch photo upload storage: %v", err)
	}
	logStore, logLocalDir, err := newUploadStore(cfg.LogUploadStoreConfig())
	if err != nil {
		log.Fatalf("init log upload storage: %v", err)
	}

	server := api.NewServer(
		cfg.HTTPAddr,
		dbClient,
		api.WithPunchPhotoObjectStore(punchPhotoStore),
		api.WithLogObjectStore(logStore),
		api.WithLocalUploadDirs(collectLocalUploadDirs(punchPhotoLocalDir, logLocalDir)...),
	)
	if err := server.Run(); err != nil {
		log.Fatalf("run server: %v", err)
	}
}

func newUploadStore(cfg config.UploadStoreConfig) (storage.ObjectStore, string, error) {
	return storage.NewStore(storage.Options{
		Backend:            cfg.StorageBackend,
		LocalDir:           cfg.LocalDir,
		PublicBaseURL:      cfg.PublicBaseURL,
		OSSEndpoint:        cfg.OSSEndpoint,
		OSSBucket:          cfg.OSSBucket,
		OSSAccessKeyID:     cfg.OSSAccessKeyID,
		OSSAccessKeySecret: cfg.OSSAccessKeySecret,
		OSSPrefix:          cfg.OSSPrefix,
	})
}

func collectLocalUploadDirs(dirs ...string) []string {
	seen := make(map[string]struct{}, len(dirs))
	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
