package transport

import (
	"context"
	"errors"
	"testing"

	controlpb "agent/api/control"
	"agent/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTaskHandlers struct {
	upsert struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		user   *domain.User
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
	allStats struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		resp   *controlpb.StatsAll
		err    error
	}
	userStats struct {
		called bool
		ctx    context.Context
		meta   *domain.Meta
		userID string
		resp   *controlpb.StatsUser
		err    error
	}
}

func (f *mockTaskHandlers) UpsertUser(ctx context.Context, meta *domain.Meta, user *domain.User) error {
	f.upsert.called = true
	f.upsert.ctx = ctx
	f.upsert.meta = meta
	f.upsert.user = user
	return f.upsert.err
}

func (f *mockTaskHandlers) RemoveUser(ctx context.Context, meta *domain.Meta, userID string, driverType string) error {
	f.remove.called = true
	f.remove.ctx = ctx
	f.remove.meta = meta
	f.remove.userID = userID
	f.remove.driverType = driverType
	return f.remove.err
}

func (f *mockTaskHandlers) GetStatsAll(ctx context.Context, meta *domain.Meta) (*controlpb.StatsAll, error) {
	f.allStats.called = true
	f.allStats.ctx = ctx
	f.allStats.meta = meta
	return f.allStats.resp, f.allStats.err
}

func (f *mockTaskHandlers) GetStatsUser(ctx context.Context, meta *domain.Meta, userID string) (*controlpb.StatsUser, error) {
	f.userStats.called = true
	f.userStats.ctx = ctx
	f.userStats.meta = meta
	f.userStats.userID = userID
	return f.userStats.resp, f.userStats.err
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
		Name:       "Alice",
		DriverType: "xray",
		Creds:      map[string]string{"token": "abc"},
	}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UpsertUser{User: user}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.upsert.called, "UpsertUser should be called")
		require.Equal(t, ctx, handler.upsert.ctx, "unexpected context")

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.upsert.meta)

		wantUser := &domain.User{
			ID:         user.GetId(),
			Name:       user.GetName(),
			DriverType: user.GetDriverType(),
			Creds:      user.GetCreds(),
		}
		assert.Equal(t, wantUser, handler.upsert.user)

		require.Len(t, sender.messages, 1)
		assertAck(t, sender.messages[0], seq)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.upsert.err = errors.New("upsert failed")
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UpsertUser{User: user}},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertNack(t, sender.messages[0], seq, handler.upsert.err.Error())
	})

	t.Run("send error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sendErr := errors.New("send failed")
		sender := &sendRecorder{errs: []error{sendErr}}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Upsert{Upsert: &controlpb.UpsertUser{User: user}},
		}

		err := RouteTask(ctx, handler, task, sender.send)
		require.ErrorIs(t, err, sendErr)
		require.Len(t, sender.messages, 1)
		assertAck(t, sender.messages[0], seq)
	})
}

func TestRouteTaskRemove(t *testing.T) {
	ctx := context.Background()
	seq := uint64(2)
	meta := &controlpb.TaskMeta{RequestId: "req-remove", Seq: seq}
	remove := &controlpb.RemoveUser{UserId: "user-2", DriverType: "slack"}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		sender := &sendRecorder{}
		task := &controlpb.Task{
			Meta: meta,
			Body: &controlpb.Task_Remove{Remove: remove},
		}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.remove.called, "RemoveUser should be called")

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.remove.meta)
		assert.Equal(t, remove.GetUserId(), handler.remove.userID)
		assert.Equal(t, remove.GetDriverType(), handler.remove.driverType)

		require.Len(t, sender.messages, 1)
		assertAck(t, sender.messages[0], seq)
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
		assertNack(t, sender.messages[0], seq, handler.remove.err.Error())
	})
}

/*func TestRouteTaskAllStats(t *testing.T) {
	ctx := context.Background()
	seq := uint64(3)
	meta := &controlpb.TaskMeta{RequestId: "req-all-stats", Seq: seq}
	task := &controlpb.Task{
		Meta: meta,
		Body: &controlpb.Task_AllStats{AllStats: &controlpb.GetStatsAll{}},
	}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.allStats.resp = &controlpb.StatsAll{TotalBytesRx: 100, TotalBytesTx: 200}
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.allStats.called, "GetStatsAll should be called")

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.allStats.meta)

		require.Len(t, sender.messages, 2)
		assert.Equal(t, handler.allStats.resp, sender.messages[0].GetAllStats())
		assertAck(t, sender.messages[1], seq)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.allStats.err = errors.New("stats failed")
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertNack(t, sender.messages[0], seq, handler.allStats.err.Error())
	})

	t.Run("send error on stats", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.allStats.resp = &controlpb.StatsAll{}
		sendErr := errors.New("stats send failed")
		sender := &sendRecorder{errs: []error{sendErr}}

		err := RouteTask(ctx, handler, task, sender.send)
		require.ErrorIs(t, err, sendErr)
		require.Len(t, sender.messages, 1)
		require.NotNil(t, sender.messages[0].GetAllStats())
	})
}

func TestRouteTaskUserStats(t *testing.T) {
	ctx := context.Background()
	seq := uint64(4)
	meta := &controlpb.TaskMeta{RequestId: "req-user-stats", Seq: seq}
	body := &controlpb.Task_UserStats{UserStats: &controlpb.GetStatsUser{UserId: "user-3"}}
	task := &controlpb.Task{Meta: meta, Body: body}

	t.Run("success", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.userStats.resp = &controlpb.StatsUser{
			Stat: &controlpb.UserStat{UserId: "user-3", BytesRx: 10, BytesTx: 20},
		}
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.True(t, handler.userStats.called, "GetStatsUser should be called")

		wantMeta := &domain.Meta{RequestID: meta.GetRequestId(), Seq: meta.GetSeq()}
		assert.Equal(t, wantMeta, handler.userStats.meta)
		assert.Equal(t, "user-3", handler.userStats.userID)

		require.Len(t, sender.messages, 2)
		assert.Equal(t, handler.userStats.resp, sender.messages[0].GetUserStats())
		assertAck(t, sender.messages[1], seq)
	})

	t.Run("handler error", func(t *testing.T) {
		handler := &mockTaskHandlers{}
		handler.userStats.err = errors.New("user stats failed")
		sender := &sendRecorder{}

		require.NoError(t, RouteTask(ctx, handler, task, sender.send))
		require.Len(t, sender.messages, 1)
		assertNack(t, sender.messages[0], seq, handler.userStats.err.Error())
	})
}*/

func TestRouteTaskUnknownTask(t *testing.T) {
	ctx := context.Background()
	seq := uint64(5)
	task := &controlpb.Task{Meta: &controlpb.TaskMeta{Seq: seq}}
	handler := &mockTaskHandlers{}
	sender := &sendRecorder{}

	require.NoError(t, RouteTask(ctx, handler, task, sender.send))
	require.Len(t, sender.messages, 1)
	assertNack(t, sender.messages[0], seq, errUnknownTask.Error())
}

func assertAck(t *testing.T, msg *controlpb.AgentToControl, seq uint64) {
	t.Helper()
	ack := msg.GetAck()
	require.NotNil(t, ack, "expected Ack message")
	assert.Equal(t, seq, ack.GetSeq())
}

func assertNack(t *testing.T, msg *controlpb.AgentToControl, seq uint64, wantErr string) {
	t.Helper()
	nack := msg.GetNack()
	require.NotNil(t, nack, "expected Nack message")
	assert.Equal(t, seq, nack.GetSeq())
	assert.Equal(t, wantErr, nack.GetError())
}
