package service

import (
	"context"
	"log/slog"
	"time"

	"agent/internal/model"
)

type Service struct {
	repo   TaskRepo
	worker *Worker
}

func NewAgentService(cfg WorkerConfig, repo TaskRepo, driver XrayDriver, sender Sender, log *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		worker: NewWorker(cfg, repo, driver, sender, log),
	}
}

func (s *Service) Run(ctx context.Context) error {
	return s.worker.Run(ctx)
}

func (s *Service) UpsertUser(ctx context.Context, requestID string, user *model.User) error {
	return s.repo.SaveTask(ctx, &model.Task{
		RequestID:  requestID,
		UserID:     user.ID,
		DriverType: user.DriverType,
		Kind:       model.TaskUpsert,
		ReceivedAt: time.Now(),
	})
}

func (s *Service) DeleteTask(ctx context.Context, requestID string, user *model.User) error {
	return s.repo.SaveTask(ctx, &model.Task{
		RequestID:  requestID,
		UserID:     user.ID,
		DriverType: user.DriverType,
		Kind:       model.TaskRemove,
		ReceivedAt: time.Now(),
	})
}
