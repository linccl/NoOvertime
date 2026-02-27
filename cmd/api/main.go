package main

import (
	"context"
	"log"

	"noovertime/config"
	"noovertime/internal/api"
	"noovertime/internal/db"
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

	server := api.NewServer(cfg.HTTPAddr, dbClient)
	if err := server.Run(); err != nil {
		log.Fatalf("run server: %v", err)
	}
}
