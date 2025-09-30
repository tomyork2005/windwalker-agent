package service

import (
	controlpb "agent/api/control"
	"agent/internal/domain"
	"context"
	"fmt"
)

type Storage interface {
	GetLastAppliedSeq(ctx context.Context) (uint64, error)
	SetLastAppliedSeq(ctx context.Context, seq uint64) error
	UpsertUser(ctx context.Context, u domain.User) error
	RemoveUser(ctx context.Context, userID string) error
}

type TxManager interface {
	WithTx(context.Context, func(ctx context.Context) error) error
}

type DriverMultiplexer interface {
	Upsert(ctx context.Context, user domain.User) error
	Remove(ctx context.Context, userID string, driverType string) error
}

type Service struct {
	storage     Storage
	tx          TxManager
	multiplexer DriverMultiplexer
}

func NewAgentService(storage Storage, tx TxManager, multiplexer DriverMultiplexer) *Service {
	return &Service{
		storage:     storage,
		tx:          tx,
		multiplexer: multiplexer,
	}
}

func (s *Service) UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) error {
	if meta == nil || user == nil || user.ID == "" {
		return fmt.Errorf("invalid upsert payload")
	}

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		last, err := s.storage.GetLastAppliedSeq(ctx)
		if err != nil {
			return err
		}
		if meta != nil && meta.Seq != 0 && meta.Seq <= last {
			// if duplicate, nothing to do --> just sending ack
			return nil
		}

		if err := s.multiplexer.Upsert(ctx, *user); err != nil {
			return fmt.Errorf("driver upsert: %w", err)
		}
		if err := s.storage.UpsertUser(ctx, *user); err != nil {
			return fmt.Errorf("db upsert: %w", err)
		}

		if meta != nil && meta.Seq != 0 {
			if err := s.storage.SetLastAppliedSeq(ctx, meta.Seq); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Service) RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error {
	if meta == nil || userID == "" || driverType == "" {
		return fmt.Errorf("invalid remove payload")
	}

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		last, err := s.storage.GetLastAppliedSeq(ctx)
		if err != nil {
			return err
		}
		if meta != nil && meta.Seq != 0 && meta.Seq <= last {
			// if duplicate, nothing to do --> just sending ack
			return nil
		}

		if err := s.multiplexer.Remove(ctx, driverType, userID); err != nil {
			return fmt.Errorf("driver remove: %w", err)
		}
		if err := s.storage.RemoveUser(ctx, userID); err != nil {
			return fmt.Errorf("db remove: %w", err)
		}

		if meta != nil && meta.Seq != 0 {
			if err := s.storage.SetLastAppliedSeq(ctx, meta.Seq); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAll, error) {
	return nil, nil
}
func (s *Service) GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUser, error) {
	return nil, nil
}
