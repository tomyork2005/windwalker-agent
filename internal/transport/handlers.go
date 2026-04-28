package transport

import (
	controlpb "agent/api/control"
	"agent/internal/domain"
	"agent/internal/logx"
	"context"
	"errors"
	"fmt"
	"time"
)

var errUnknownTask = errors.New("unknown task")

type TaskHandlers interface {
	UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) (*controlpb.UserCreds, error)
	RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error
	RenewUser(ctx context.Context, meta *domain.Meta, userID, driverType string, expiresAt time.Time) error
	GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAllResponse, error)
	GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUserResponse, error)
}

func RouteTask(
	ctx context.Context,
	handler TaskHandlers,
	task *controlpb.Task,
	send func(*controlpb.AgentToControl) error,
) error {
	if handler == nil {
		return fmt.Errorf("transport: TaskHandlers is required")
	}
	meta := fromProtoToServiceMeta(task.GetMeta())
	log := logx.With("component", "transport", "seq", meta.Seq)

	switch body := task.Body.(type) {
	case *controlpb.Task_Upsert:
		log.Info("task received",
			"op", "UPSERT",
			"driver_type", body.Upsert.GetUser().GetDriverType(),
			"user_id", body.Upsert.GetUser().GetId(),
		)

		creds, err := handler.UpsertUser(ctx, meta, fromProtoToServiceUser(body.Upsert.GetUser()))
		if err != nil {
			return sendResp(send, meta.Seq, &controlpb.Response_Error{
				Error: &controlpb.Error{Error: err.Error()},
			})
		}
		return sendResp(send, meta.Seq, &controlpb.Response_Upsert{
			Upsert: &controlpb.UserUpsertResponse{Creds: creds},
		})

	case *controlpb.Task_Remove:
		log.Info("task received",
			"op", "REMOVE",
			"driver_type", body.Remove.GetDriverType(),
			"user_id", body.Remove.GetUserId(),
		)

		if err := handler.RemoveUser(ctx, meta, body.Remove.GetUserId(), body.Remove.GetDriverType()); err != nil {
			return sendResp(send, meta.Seq, &controlpb.Response_Error{
				Error: &controlpb.Error{Error: err.Error()},
			})
		}
		return sendResp(send, meta.Seq, &controlpb.Response_Remove{
			Remove: &controlpb.UserRemoveResponse{},
		})

	case *controlpb.Task_Renew:
		var expires time.Time
		if ts := body.Renew.GetExpiresAt(); ts != nil && ts.CheckValid() == nil {
			expires = ts.AsTime()
		}
		log.Info("task received",
			"op", "RENEW",
			"driver_type", body.Renew.GetDriverType(),
			"user_id", body.Renew.GetUserId(),
			"expires_at", expires,
		)

		if err := handler.RenewUser(ctx, meta, body.Renew.GetUserId(), body.Renew.GetDriverType(), expires); err != nil {
			return sendResp(send, meta.Seq, &controlpb.Response_Error{
				Error: &controlpb.Error{Error: err.Error()},
			})
		}
		return sendResp(send, meta.Seq, &controlpb.Response_Renew{
			Renew: &controlpb.UserRenewResponse{},
		})

	case *controlpb.Task_StatsAll:
		log.Info("task received", "op", "STATS_ALL")

		resp, err := handler.GetStatsAll(ctx, meta)
		if err != nil {
			return sendResp(send, meta.Seq, &controlpb.Response_Error{
				Error: &controlpb.Error{Error: err.Error()},
			})
		}
		return sendResp(send, meta.Seq, &controlpb.Response_StatsAll{StatsAll: resp})

	case *controlpb.Task_StatsUser:
		log.Info("task received", "op", "STATS_USER", "user_id", body.StatsUser.GetUserId())

		resp, err := handler.GetStatsUser(ctx, meta, body.StatsUser.GetUserId())
		if err != nil {
			return sendResp(send, meta.Seq, &controlpb.Response_Error{
				Error: &controlpb.Error{Error: err.Error()},
			})
		}
		return sendResp(send, meta.Seq, &controlpb.Response_StatsUser{StatsUser: resp})

	default:
		return sendResp(send, meta.Seq, &controlpb.Response_Error{
			Error: &controlpb.Error{Error: errUnknownTask.Error()},
		})
	}
}

// sendResp wraps a oneof body variant (Response_Upsert / _Remove / _StatsAll /
// _StatsUser / _Error) into a Response with Meta and ships it via the send queue.
// The body is one of the generated oneof wrapper structs — we rely on them
// implementing the unexported isResponse_Body interface, which is why we
// type-switch here instead of taking a typed parameter.
func sendResp(send func(*controlpb.AgentToControl) error, seq uint64, body any) error {
	resp := &controlpb.Response{Meta: &controlpb.TaskMeta{Seq: seq}}
	switch b := body.(type) {
	case *controlpb.Response_Upsert:
		resp.Body = b
	case *controlpb.Response_Remove:
		resp.Body = b
	case *controlpb.Response_Renew:
		resp.Body = b
	case *controlpb.Response_StatsAll:
		resp.Body = b
	case *controlpb.Response_StatsUser:
		resp.Body = b
	case *controlpb.Response_Error:
		resp.Body = b
	default:
		return fmt.Errorf("transport: unsupported response body %T", body)
	}
	return send(&controlpb.AgentToControl{
		Msg: &controlpb.AgentToControl_Resp{Resp: resp},
	})
}

func fromProtoToServiceUser(proto *controlpb.User) *domain.User {
	u := &domain.User{
		ID:         proto.GetId(),
		AccountID:  proto.GetAccountId(),
		DriverType: proto.GetDriverType(),
	}

	if ts := proto.GetExpiresAt(); ts != nil && ts.CheckValid() == nil {
		u.ExpiresAt = ts.AsTime()
	}

	return u
}

func fromProtoToServiceMeta(meta *controlpb.TaskMeta) *domain.Meta {
	if meta == nil {
		return &domain.Meta{}
	}
	return &domain.Meta{
		RequestID: meta.GetRequestId(),
		Seq:       meta.GetSeq(),
	}
}
