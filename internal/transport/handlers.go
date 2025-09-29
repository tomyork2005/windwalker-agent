package transport

import (
	controlpb "agent/api/control"
	"agent/internal/domain"
	"context"
	"errors"
	"fmt"
)

var errUnknownTask = errors.New("Unknown task")

type TaskHandlers interface {
	UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) error
	RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error
	GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAll, error)
	GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUser, error)
}

func RouteTask(
	ctx context.Context,
	h TaskHandlers,
	task *controlpb.Task,
	send func(*controlpb.AgentToControl) error,
) error {
	if h == nil {
		return fmt.Errorf("transport: TaskHandlers is required")
	}
	meta := fromProtoToServiceMeta(task.GetMeta())

	switch body := task.Body.(type) {
	case *controlpb.Task_Upsert:
		if err := h.UpsertUser(ctx, meta, fromProtoToServiceUser(body.Upsert.GetUser())); err != nil {
			return sendNack(send, meta.Seq, err)
		}
		return sendAck(send, meta.Seq)
	case *controlpb.Task_Remove:
		if err := h.RemoveUser(ctx, meta, body.Remove.GetUserId(), body.Remove.GetDriverType()); err != nil {
			return sendNack(send, meta.Seq, err)
		}
		return sendAck(send, meta.Seq)

	case *controlpb.Task_AllStats:
		resp, err := h.GetStatsAll(ctx, meta)
		if err != nil {
			return sendNack(send, meta.Seq, err)
		}
		if err := send(&controlpb.AgentToControl{
			Msg: &controlpb.AgentToControl_AllStats{AllStats: resp},
		}); err != nil {
			return err
		}
		return sendAck(send, meta.Seq)

	case *controlpb.Task_UserStats:
		resp, err := h.GetStatsUser(ctx, meta, body.UserStats.GetUserId())
		if err != nil {
			return sendNack(send, meta.Seq, err)
		}
		if err := send(&controlpb.AgentToControl{
			Msg: &controlpb.AgentToControl_UserStats{UserStats: resp},
		}); err != nil {
			return err
		}
		return sendAck(send, meta.Seq)

	default:
		return sendNack(send, meta.Seq, errUnknownTask)
	}

}

func sendAck(send func(*controlpb.AgentToControl) error, seq uint64) error {
	return send(&controlpb.AgentToControl{
		Msg: &controlpb.AgentToControl_Ack{Ack: &controlpb.Ack{Seq: seq}},
	})
}

func sendNack(send func(*controlpb.AgentToControl) error, seq uint64, err error) error {
	return send(&controlpb.AgentToControl{
		Msg: &controlpb.AgentToControl_Nack{
			Nack: &controlpb.Nack{Seq: seq, Error: err.Error()},
		},
	})
}

func fromProtoToServiceUser(proto *controlpb.User) *domain.User {
	u := &domain.User{
		ID:         proto.GetId(),
		Name:       proto.GetName(),
		DriverType: proto.GetDriverType(),
		Creds:      proto.GetCreds(),
	}

	if ts := proto.GetExpiresAt(); ts != nil && ts.CheckValid() == nil {
		u.ExpiresAt = ts.AsTime()
	}

	return u
}

func fromProtoToServiceMeta(meta *controlpb.TaskMeta) *domain.Meta {
	return &domain.Meta{
		RequestID: meta.GetRequestId(),
		Seq:       meta.GetSeq(),
	}
}
