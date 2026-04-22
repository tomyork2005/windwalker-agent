package main

import (
	"agent/internal/driver"
	"agent/internal/logx"
	"agent/internal/service"
	"agent/internal/storage"
	"agent/internal/transport"
	"context"
	"os"
	"time"

	"agent/internal/config"
)

func main() {
	ctx := context.Background()
	cfg := config.MustLoadConfig()

	sqliteStorage, err := storage.NewSQLiteStorage(ctx, cfg.SQLiteConfig)
	if err != nil {
		logx.Error("storage start failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		err := sqliteStorage.Close()
		if err != nil {
			logx.Error("Failed to close sqlite storage")
		}
	}()
	logx.Info("storage started")

	xrayDriver := driver.NewXrayDriver(cfg.XrayConfig)
	err = xrayDriver.Start(ctx)
	if err != nil {
		logx.Error("xray start failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = xrayDriver.Stop(shCtx)
	}()
	logx.Info("xray started - name - ", xrayDriver.Name())

	multiplexer := driver.NewMultiplexer(xrayDriver)
	agentService := service.NewAgentService(sqliteStorage, sqliteStorage.GetTxManager(), multiplexer)

	transportClient := transport.NewClient(cfg.TransportGrpcConfig)
	err = transportClient.Run(ctx, agentService)
	logx.Info("transport success started")
	if err != nil {
		logx.Error("transport run failed", "err", err)
		os.Exit(1)
	}
}
