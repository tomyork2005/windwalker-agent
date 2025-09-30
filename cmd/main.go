package main

import (
	"agent/internal/driver"
	"agent/internal/service"
	"agent/internal/storage"
	"agent/internal/transport"
	"context"
	"os"

	"agent/internal/config"
)

func main() {
	ctx := context.Background()

	cfg := config.MustLoadConfig()

	sqliteStorage, err := storage.NewSQLiteStorage(ctx, cfg.SQLiteConfig)
	if err != nil {
		os.Exit(1)
	}

	xrayDriver := driver.NewXrayDriver(cfg.XrayConfig)

	err = xrayDriver.Start(ctx)
	if err != nil {
		os.Exit(1)
	}

	multiplexer := driver.NewMultiplexer(xrayDriver)
	agentService := service.NewAgentService(sqliteStorage, sqliteStorage.GetTxManager(), multiplexer)

	transportClient := transport.NewClient(cfg.TransportGrpcConfig)
	err = transportClient.Run(ctx, agentService)
	if err != nil {
		os.Exit(1)
	}
}
