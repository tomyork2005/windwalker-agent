package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type mockTaskHandlers struct {
	upsert struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		user   *domain.User
		resp   *controlpb.UserCreds
		err    error
	}
	remove struct {
		called     bool
		ctx        context.Context
		meta       *domain.Meta
		userID     string
		driverType string
		err        error
	}
	statsAll struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		resp   *controlpb.StatsAllResponse
		err    error
	}
	statsUser struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		userID string
		resp   *controlpb.StatsUserResponse
		err    error
	}
	renew struct {
		called     bool
		ctx        context.Context
		meta       *domain.Meta
		userID     string
		driverType string
		expiresAt  time.Time
		err        error
	}
}

func (f *mockTaskHandlers) UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) (*controlpb.UserCreds, error) {
	f.upsert.called = true
	f.upsert.ctx = ctx
	f.upsert.meta = meta
	f.upsert.user = user
	return f.upsert.resp, f.upsert.err
}

func (f *mockTaskHandlers) RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error {
	f.remove.called = true
	f.remove.ctx = ctx
	f.remove.meta = meta
	f.remove.userID = userID
	f.remove.driverType = driverType
	return f.remove.err
}

func (f *mockTaskHandlers) RenewUser(ctx context.Context, meta *domain.Meta, userID, driverType string, expiresAt time.Time) error {
	f.renew.called = true
	f.renew.ctx = ctx
	f.renew.meta = meta
	f.renew.userID = userID
	f.renew.driverType = driverType
	f.renew.expiresAt = expiresAt
	return f.renew.err
}

func (f *mockTaskHandlers) GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAllResponse, error) {
	f.statsAll.called = true
	f.statsAll.ctx = ctx
	f.statsAll.meta = meta
	return f.statsAll.resp, f.statsAll.err
}

func (f *mockTaskHandlers) GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUserResponse, error) {
	f.statsUser.called = true
	f.statsUser.ctx = ctx
	f.statsUser.meta = meta
	f.statsUser.userID = userID
	return f.statsUser.resp, f.statsUser.err
}

type sendRecorder struct {
	messages []*controlpb.AgentToControl
	errs     []error
}

func (s *sendRecorder) send(msg *controlpb.AgentToControl) error {
	s.messages = append(s.messages, msg)
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}

func responseFrom(t *testing.T, msg *controlpb.AgentToControl) *controlpb.Response {
	t.Helper()
	require.NotNil(t, msg)
	wrap, ok := msg.Msg.(*controlpb.AgentToControl_Resp)
	require.True(t, ok, "expected AgentToControl_Resp, got %T", msg.Msg)
	require.NotNil(t, wrap.Resp)
	return wrap.Resp
}

func assertError(t *testing.T, msg *controlpb.AgentToControl, seq uint64, wantErr string) {
	t.Helper()
	resp := responseFrom(t, msg)
	assert.Equal(t, seq, resp.GetMeta().GetSeq())
	errBody, ok := resp.Body.(*controlpb.Response_Error)
	require.True(t, ok, "expected Response_Error, got %T", resp.Body)
	assert.Equal(t, wantErr, errBody.Error.GetError())
}

func TestRouteTaskNilHandler(t *testing.T) {
	sender := &sendRecorder{}
	task := &controlpb.Task{}

	err := RouteTask(context.Background(), nil, task, sender.send)
	require.Error(t, err)
	require.EqualError(t, err, "transport: TaskHandlers is required")
	assert.Len(t, sender.messages, 0, "send must not be called")
}

func TestRouteTaskUpsert(t *testing.T) {
	ctx := context.Background()
	seq := uint64(1)
	meta := &controlpb.TaskMeta{RequestId: "req-upsert", Seq: seq}
	user := &controlpb.User{
		Id:         "user-1",
		AccountId:  "acc-1",
		DriverType: "xray",
	}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.upsert.resp = &controlpb.UserCreds{UserId: "user-1", DriverType: "xray"}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UserUpsertRequest{User: user}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.upsert.called)
		require.Equal(t, ctx, handler.upsert.ctx)

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.upsert.meta)

		wantUser := &domain.User{
			ID:         user.GetId(),
			AccountID:  user.GetAccountId(),
			DriverType: user.GetDriverType(),
		}
		assert.Equal(t, wantUser, handler.upsert.user)

		require.Len(t, sender.messages, 1)
		resp := responseFrom(t, sender.messages[0])
		assert.Equal(t, seq, resp.GetMeta().GetSeq())
		upsertBody, ok := resp.Body.(*controlpb.Response_Upsert)
		require.True(t, ok, "expected Response_Upsert, got %T", resp.Body)
		assert.Equal(t, handler.upsert.resp, upsertBody.Upsert.GetCreds())
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.upsert.err = errors.New("upsert failed")
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UserUpsertRequest{User: user}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertError(t, sender.messages[0], seq, "upsert failed")
	})

	t.Run("send error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.upsert.resp = &controlpb.UserCreds{UserId: "user-1"}
		sendErr := errors.New("send failed")
		sender := &sendRecorder{errs: []error{sendErr}}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UserUpsertRequest{User: user}},
		}

		err := RouteTask(ctx, handler, task, sender.send)
		require.ErrorIs(t, err, sendErr)
		require.Len(t, sender.messages, 1)
	})
}

func TestRouteTaskRemove(t *testing.T) {
	ctx := context.Background()
	seq := uint64(2)
	meta := &controlpb.TaskMeta{RequestId: "req-remove", Seq: seq}
	remove := &controlpb.UserRemoveRequest{UserId: "user-2", DriverType: "xray"}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Remove{Remove: remove},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.remove.called)

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.remove.meta)
		assert.Equal(t, remove.GetUserId(), handler.remove.userID)
		assert.Equal(t, remove.GetDriverType(), handler.remove.driverType)

		require.Len(t, sender.messages, 1)
		resp := responseFrom(t, sender.messages[0])
		assert.Equal(t, seq, resp.GetMeta().GetSeq())
		_, ok := resp.Body.(*controlpb.Response_Remove)
		require.True(t, ok, "expected Response_Remove, got %T", resp.Body)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.remove.err = errors.New("remove failed")
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Remove{Remove: remove},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertError(t, sender.messages[0], seq, "remove failed")
	})
}

func TestRouteTaskRenew(t *testing.T) {
	ctx := context.Background()
	seq := uint64(6)
	meta := &controlpb.TaskMeta{RequestId: "req-renew", Seq: seq}
	expires := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Renew{Renew: &controlpb.UserRenewRequest{
				UserId:     "user-7",
				DriverType: "xray",
				ExpiresAt:  timestamppb.New(expires),
			}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.renew.called)

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.renew.meta)
		assert.Equal(t, "user-7", handler.renew.userID)
		assert.Equal(t, "xray", handler.renew.driverType)
		assert.True(t, handler.renew.expiresAt.Equal(expires))

		require.Len(t, sender.messages, 1)
		resp := responseFrom(t, sender.messages[0])
		assert.Equal(t, seq, resp.GetMeta().GetSeq())
		_, ok := resp.Body.(*controlpb.Response_Renew)
		require.True(t, ok, "expected Response_Renew, got %T", resp.Body)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.renew.err = errors.New("renew failed")
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Renew{Renew: &controlpb.UserRenewRequest{
				UserId:     "user-7",
				DriverType: "xray",
				ExpiresAt:  timestamppb.New(expires),
			}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertError(t, sender.messages[0], seq, "renew failed")
	})

	t.Run("nil expires_at", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Renew{Renew: &controlpb.UserRenewRequest{
				UserId:     "user-7",
				DriverType: "xray",
				ExpiresAt:  nil,
			}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.renew.called)
		assert.True(t, handler.renew.expiresAt.IsZero(), "nil proto timestamp must map to zero time.Time")
	})
}

func TestRouteTaskStatsAll(t *testing.T) {
	ctx := context.Background()
	seq := uint64(3)
	meta := &controlpb.TaskMeta{RequestId: "req-stats-all", Seq: seq}
	task := &controlpb.Task{
		Meta: meta,
		Body: &controlpb.Task_StatsAll{StatsAll: &controlpb.StatsAllRequest{}},
	}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.statsAll.resp = &controlpb.StatsAllResponse{TotalBytesRx: 100, TotalBytesTx: 200}
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.statsAll.called)

		require.Len(t, sender.messages, 1)
		resp := responseFrom(t, sender.messages[0])
		body, ok := resp.Body.(*controlpb.Response_StatsAll)
		require.True(t, ok, "expected Response_StatsAll, got %T", resp.Body)
		assert.Equal(t, handler.statsAll.resp, body.StatsAll)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.statsAll.err = errors.New("stats failed")
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertError(t, sender.messages[0], seq, "stats failed")
	})
}

func TestRouteTaskStatsUser(t *testing.T) {
	ctx := context.Background()
	seq := uint64(4)
	meta := &controlpb.TaskMeta{RequestId: "req-stats-user", Seq: seq}
	task := &controlpb.Task{
		Meta: meta,
		Body: &controlpb.Task_StatsUser{StatsUser: &controlpb.StatsUserRequest{UserId: "user-3"}},
	}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.statsUser.resp = &controlpb.StatsUserResponse{
			Stat: &controlpb.UserStat{UserId: "user-3", BytesRx: 10, BytesTx: 20},
		}
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.statsUser.called)
		assert.Equal(t, "user-3", handler.statsUser.userID)

		require.Len(t, sender.messages, 1)
		resp := responseFrom(t, sender.messages[0])
		body, ok := resp.Body.(*controlpb.Response_StatsUser)
		require.True(t, ok, "expected Response_StatsUser, got %T", resp.Body)
		assert.Equal(t, handler.statsUser.resp, body.StatsUser)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.statsUser.err = errors.New("user stats failed")
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertError(t, sender.messages[0], seq, "user stats failed")
	})
}

func TestRouteTaskUnknownTask(t *testing.T) {
	ctx := context.Background()
	seq := uint64(5)
	task := &controlpb.Task{Meta: &controlpb.TaskMeta{Seq: seq}}
	handler := &mockTaskHandlers{}
	sender := &sendRecorder{}

	require.NoError(t, RouteTask(ctx, handler, task, sender.send))
	require.Len(t, sender.messages, 1)
	assertError(t, sender.messages[0], seq, errUnknownTask.Error())
}
