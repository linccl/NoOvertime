package main

import (
	"context"
	"log"

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

	objectStore, localUploadDir, err := storage.NewStore(storage.Options{
		Backend:            cfg.UploadStorageBackend,
		LocalDir:           cfg.UploadLocalDir,
		PublicBaseURL:      cfg.UploadPublicBaseURL,
		OSSEndpoint:        cfg.UploadOSSEndpoint,
		OSSBucket:          cfg.UploadOSSBucket,
		OSSAccessKeyID:     cfg.UploadOSSAccessKeyID,
		OSSAccessKeySecret: cfg.UploadOSSAccessKeySecret,
		OSSPrefix:          cfg.UploadOSSPrefix,
	})
	if err != nil {
		log.Fatalf("init upload storage: %v", err)
	}

	server := api.NewServer(
		cfg.HTTPAddr,
		dbClient,
		api.WithObjectStore(objectStore),
		api.WithLocalUploadDir(localUploadDir),
	)
	if err := server.Run(); err != nil {
		log.Fatalf("run server: %v", err)
	}
}
