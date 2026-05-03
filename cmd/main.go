package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	"agent/internal/config"
	"agent/internal/drivers/xray"
	"agent/internal/service"
	"agent/internal/storage"
	"agent/internal/transport"
)

func main() {
	cfg := config.MustLoadConfig()

	log := newLogger(cfg.Env)
	slog.SetDefault(log)

	log.Info("agent starting",
		"agent_id", cfg.AgentID,
		"instance_id", cfg.Transport.InstanceID,
		"env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.New(ctx, cfg.Storage, log)
	if err != nil {
		log.Error("storage init failed", "err", err)
		os.Exit(1)
	}
	defer closeWithLog(log, "storage", store.Close)

	drv, err := xray.New(ctx, cfg.DriverXray, log)
	if err != nil {
		log.Error("xray driver init failed", "err", err)
		os.Exit(1)
	}
	defer closeWithLog(log, "xray driver", drv.Close)

	tport := transport.New(cfg.Transport, log)
	svc := service.NewAgentService(service.WorkerConfig(cfg.Worker), store, drv, tport, log)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return svc.Run(gctx) })
	g.Go(func() error { return tport.Run(gctx, svc) })

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("agent stopped with error", "err", err)
		os.Exit(1)
	}
	log.Info("agent stopped cleanly")
}

func newLogger(env string) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(env, "dev") || strings.EqualFold(env, "local") {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func closeWithLog(log *slog.Logger, name string, fn func() error) {
	if err := fn(); err != nil {
		log.Error("close failed", "component", name, "err", err)
	}
}
