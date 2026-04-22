package service

import (
	controlpb "agent/api/control"
	"agent/internal/domain"
	"agent/internal/logx"
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
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
	BuildCreds(user domain.User) (*controlpb.UserCreds, error)
}

type Service struct {
	storage     Storage
	tx          TxManager
	multiplexer DriverMultiplexer
	agentID     string
}

func NewAgentService(storage Storage, tx TxManager, multiplexer DriverMultiplexer) *Service {
	return &Service{
		storage:     storage,
		tx:          tx,
		multiplexer: multiplexer,
	}
}

// SetAgentID attaches agent_id (received from control-plane in Welcome)
// so it can be stamped on UserCreds responses.
func (s *Service) SetAgentID(id string) { s.agentID = id }

func (s *Service) UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) (*controlpb.UserCreds, error) {
	if meta == nil || user == nil || user.ID == "" {
		return nil, fmt.Errorf("invalid upsert request payload")
	}

	log := logx.With(
		"component", "service",
		"op", "upsert",
		"seq", meta.Seq,
		"user_id", user.ID,
	)
	log.Debug("starting user upsert")

	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		last, err := s.storage.GetLastAppliedSeq(ctx)
		if err != nil {
			log.Error("storage read failed", "err", err)
			return fmt.Errorf("storage get last applied seq: %w", err)
		}
		if meta.Seq != 0 && meta.Seq <= last {
			log.Info("skip duplicated task")
			return nil
		}

		if err := s.multiplexer.Upsert(ctx, *user); err != nil {
			log.Error("driver upsert failed", "err", err)
			return fmt.Errorf("driver upsert user: %w", err)
		}
		if err := s.storage.UpsertUser(ctx, *user); err != nil {
			log.Error("storage upsert failed", "err", err)
			return fmt.Errorf("storage upsert user: %w", err)
		}

		if meta.Seq != 0 {
			if err := s.storage.SetLastAppliedSeq(ctx, meta.Seq); err != nil {
				log.Error("storage set last seq failed", "err", err)
				return fmt.Errorf("storage set last applied seq: %w", err)
			}
		}

		log.Info("user upserted successfully")
		return nil
	})
	if err != nil {
		return nil, err
	}

	creds, err := s.multiplexer.BuildCreds(*user)
	if err != nil {
		log.Error("build creds failed", "err", err)
		return nil, fmt.Errorf("build creds: %w", err)
	}
	creds.AgentId = s.agentID
	creds.GeneratedAt = timestamppb.Now()
	return creds, nil
}

func (s *Service) RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error {
	if meta == nil || userID == "" || driverType == "" {
		return fmt.Errorf("invalid remove request payload")
	}

	log := logx.With(
		"component", "service",
		"op", "remove",
		"seq", meta.Seq,
		"user_id", userID,
		"driver_type", driverType,
	)
	log.Debug("starting user remove")

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		last, err := s.storage.GetLastAppliedSeq(ctx)
		if err != nil {
			log.Error("storage read failed", "err", err)
			return fmt.Errorf("storage get last applied seq: %w", err)
		}
		if meta.Seq != 0 && meta.Seq <= last {
			log.Info("skip duplicated task")
			return nil
		}

		if err := s.multiplexer.Remove(ctx, userID, driverType); err != nil {
			log.Error("driver remove failed", "err", err)
			return fmt.Errorf("driver remove user: %w", err)
		}
		if err := s.storage.RemoveUser(ctx, userID); err != nil {
			log.Error("storage remove failed", "err", err)
			return fmt.Errorf("storage remove user: %w", err)
		}

		if meta.Seq != 0 {
			if err := s.storage.SetLastAppliedSeq(ctx, meta.Seq); err != nil {
				log.Error("storage set last seq failed", "err", err)
				return fmt.Errorf("storage set last applied seq: %w", err)
			}
		}
		log.Info("user removed successfully")

		return nil
	})
}

func (s *Service) GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAllResponse, error) {
	return &controlpb.StatsAllResponse{}, nil
}

func (s *Service) GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUserResponse, error) {
	return &controlpb.StatsUserResponse{Stat: &controlpb.UserStat{UserId: userID}}, nil
}
