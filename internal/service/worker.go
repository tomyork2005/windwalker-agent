package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"agent/internal/model"

	"golang.org/x/sync/errgroup"
)

type XrayDriver interface {
	AddUser(ctx context.Context, userID string) error
	RemoveUser(ctx context.Context, userID string) error
	ListUsers(ctx context.Context) ([]string, error)
	CollectStats(ctx context.Context) ([]*model.UserUsage, error)
	BuildCreds(userID string) (string, error)
}

type TaskRepo interface {
	SaveTask(ctx context.Context, task *model.Task) error
	PullPendingTasks(ctx context.Context, limit int) ([]*model.Task, error)
	MarkDone(ctx context.Context, requestID string) error
	MarkFailed(ctx context.Context, requestID string, errMsg string) error
	ListUserIDs(ctx context.Context) ([]string, error)
	UpsertUser(ctx context.Context, userID, driverType string) error
	DeleteUser(ctx context.Context, userID, driverType string) error
}

type Sender interface {
	SendUpsertAck(ctx context.Context, msg *model.OutboundUpsert) error
	SendRemoveAck(ctx context.Context, msg *model.OutboundRemove) error
	SendError(ctx context.Context, msg *model.OutboundError) error
	SendStats(ctx context.Context, msg *model.OutboundStats) error
}

type WorkerConfig struct {
	TaskPollInterval time.Duration
	StatsInterval    time.Duration
	SyncInterval     time.Duration
	TaskBatchLimit   int
	AgentID          string
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		TaskPollInterval: time.Second,
		StatsInterval:    30 * time.Second,
		SyncInterval:     time.Minute,
		TaskBatchLimit:   32,
	}
}

type Worker struct {
	cfg    WorkerConfig
	repo   TaskRepo
	driver XrayDriver
	sender Sender
	log    *slog.Logger
	start  time.Time
}

func NewWorker(cfg WorkerConfig, repo TaskRepo, driver XrayDriver, sender Sender, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		cfg:    cfg,
		repo:   repo,
		driver: driver,
		sender: sender,
		log:    log.With("component", "worker"),
		start:  time.Now(),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return w.runTaskLoop(gctx) })
	g.Go(func() error { return w.runStatsLoop(gctx) })
	g.Go(func() error { return w.runSyncLoop(gctx) })
	return g.Wait()
}

func (w *Worker) runTaskLoop(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.TaskPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.tickTask(ctx)
		}
	}
}

func (w *Worker) tickTask(ctx context.Context) {
	tasks, err := w.repo.PullPendingTasks(ctx, w.cfg.TaskBatchLimit)
	if err != nil {
		w.log.Error("pull pending tasks", "err", err)
		return
	}
	for _, t := range tasks {
		w.processTask(ctx, t)
	}
}

func (w *Worker) processTask(ctx context.Context, t *model.Task) {
	var err error

	switch t.Kind {
	case model.TaskUpsert:
		err = w.handleUpsert(ctx, t)
	case model.TaskRemove:
		err = w.handleRemove(ctx, t)
	default:
		err = fmt.Errorf("unknown task kind: %q", t.Kind)
	}
	if err != nil {
		w.log.Error("task failed", "request_id", t.RequestID, "kind", t.Kind, "err", err)
		if sendErr := w.sender.SendError(ctx, &model.OutboundError{
			RequestID: t.RequestID,
			Error:     err.Error(),
		}); sendErr != nil {
			w.log.Error("send error ack", "request_id", t.RequestID, "err", sendErr)
		}
		if markErr := w.repo.MarkFailed(ctx, t.RequestID, err.Error()); markErr != nil {
			w.log.Error("mark failed", "request_id", t.RequestID, "err", markErr)
		}
		return
	}

	if markErr := w.repo.MarkDone(ctx, t.RequestID); markErr != nil {
		w.log.Error("mark done", "request_id", t.RequestID, "err", markErr)
	}
}

func (w *Worker) handleUpsert(ctx context.Context, t *model.Task) error {
	if err := w.driver.AddUser(ctx, t.UserID); err != nil {
		return fmt.Errorf("add user: %w", err)
	}

	creds, err := w.driver.BuildCreds(t.UserID)
	if err != nil {
		return fmt.Errorf("build creds: %w", err)
	}

	if err := w.repo.UpsertUser(ctx, t.UserID, t.DriverType); err != nil {
		return fmt.Errorf("repo upsert user: %w", err)
	}

	if err := w.sender.SendUpsertAck(ctx, &model.OutboundUpsert{
		RequestID:   t.RequestID,
		UserID:      t.UserID,
		AgentID:     w.cfg.AgentID,
		DriverType:  t.DriverType,
		VlessURI:    creds,
		GeneratedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("send upsert ack: %w", err)
	}

	return nil
}

func (w *Worker) handleRemove(ctx context.Context, t *model.Task) error {
	if err := w.driver.RemoveUser(ctx, t.UserID); err != nil {
		return fmt.Errorf("remove user: %w", err)
	}

	if err := w.repo.DeleteUser(ctx, t.UserID, t.DriverType); err != nil {
		return fmt.Errorf("repo delete user: %w", err)
	}

	if err := w.sender.SendRemoveAck(ctx, &model.OutboundRemove{
		RequestID: t.RequestID,
	}); err != nil {
		return fmt.Errorf("send remove ack: %w", err)
	}

	return nil
}

func (w *Worker) runStatsLoop(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.StatsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.tickStats(ctx)
		}
	}
}

func (w *Worker) tickStats(ctx context.Context) {
	cur, err := w.driver.CollectStats(ctx)
	if err != nil {
		w.log.Error("collect stats", "err", err)
		return
	}

	users := make([]model.UserUsage, 0, len(cur))
	for _, u := range cur {
		if u == nil {
			continue
		}
		users = append(users, *u)
	}

	if err := w.sender.SendStats(ctx, &model.OutboundStats{
		AgentID:       w.cfg.AgentID,
		UptimeSeconds: uint32(time.Since(w.start).Seconds()),
		WindowEnd:     time.Now(),
		Users:         users,
	}); err != nil {
		w.log.Error("send stats", "err", err)
	}
}

func (w *Worker) runSyncLoop(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.tickSync(ctx)
		}
	}
}

func (w *Worker) tickSync(ctx context.Context) {
	inXray, err := w.driver.ListUsers(ctx)
	if err != nil {
		w.log.Error("xray list users", "err", err)
		return
	}
	inDB, err := w.repo.ListUserIDs(ctx)
	if err != nil {
		w.log.Error("db list users", "err", err)
		return
	}

	toAdd, toRemove := diffUserSets(inDB, inXray)
	for _, id := range toAdd {
		if err := w.driver.AddUser(ctx, id); err != nil {
			w.log.Error("sync add user", "user_id", id, "err", err)
		}
	}
	for _, id := range toRemove {
		if err := w.driver.RemoveUser(ctx, id); err != nil {
			w.log.Error("sync remove user", "user_id", id, "err", err)
		}
	}
}

func diffUserSets(inDB, inXray []string) (add, remove []string) {
	db := toSet(inDB)
	xr := toSet(inXray)
	for _, id := range inDB {
		if !xr[id] {
			add = append(add, id)
		}
	}
	for _, id := range inXray {
		if !db[id] {
			remove = append(remove, id)
		}
	}
	return add, remove
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
