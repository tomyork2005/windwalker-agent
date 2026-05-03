package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"agent/internal/model"
	"agent/internal/service/mocks"

	"github.com/gojuno/minimock/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workerMocks struct {
	driver *mocks.XrayDriverMock
	repo   *mocks.TaskRepoMock
	sender *mocks.SenderMock
}

func newWorkerMocks(t *testing.T) *workerMocks {
	t.Helper()
	mc := minimock.NewController(t)
	return &workerMocks{
		driver: mocks.NewXrayDriverMock(mc),
		repo:   mocks.NewTaskRepoMock(mc),
		sender: mocks.NewSenderMock(mc),
	}
}

func newTestWorker(m *workerMocks, agentID string) *Worker {
	cfg := DefaultWorkerConfig()
	cfg.AgentID = agentID
	return NewWorker(cfg, m.repo, m.driver, m.sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestProcessTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errBoom := errors.New("boom")

	upsertTask := &model.Task{
		RequestID:  "req-upsert",
		UserID:     "user-1",
		DriverType: "xray",
		Kind:       model.TaskUpsert,
	}
	removeTask := &model.Task{
		RequestID:  "req-remove",
		UserID:     "user-2",
		DriverType: "xray",
		Kind:       model.TaskRemove,
	}

	tests := []struct {
		name  string
		task  *model.Task
		setup func(t *testing.T, m *workerMocks)
	}{
		{
			name: "upsert: happy path",
			task: upsertTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.AddUserMock.Expect(minimock.AnyContext, "user-1").Return(nil)
				m.driver.BuildCredsMock.Expect("user-1").Return("vless://creds", nil)
				m.repo.UpsertUserMock.Expect(minimock.AnyContext, "user-1", "xray").Return(nil)
				m.sender.SendUpsertAckMock.Set(func(_ context.Context, msg *model.OutboundUpsert) error {
					assert.Equal(t, "req-upsert", msg.RequestID)
					assert.Equal(t, "user-1", msg.UserID)
					assert.Equal(t, "agent-X", msg.AgentID)
					assert.Equal(t, "xray", msg.DriverType)
					assert.Equal(t, "vless://creds", msg.VlessURI)
					assert.WithinDuration(t, time.Now(), msg.GeneratedAt, time.Second)
					return nil
				})
				m.repo.MarkDoneMock.Expect(minimock.AnyContext, "req-upsert").Return(nil)
			},
		},
		{
			name: "remove: happy path",
			task: removeTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.RemoveUserMock.Expect(minimock.AnyContext, "user-2").Return(nil)
				m.repo.DeleteUserMock.Expect(minimock.AnyContext, "user-2", "xray").Return(nil)
				m.sender.SendRemoveAckMock.Set(func(_ context.Context, msg *model.OutboundRemove) error {
					assert.Equal(t, "req-remove", msg.RequestID)
					return nil
				})
				m.repo.MarkDoneMock.Expect(minimock.AnyContext, "req-remove").Return(nil)
			},
		},
		{
			name: "upsert: AddUser fails -> SendError + MarkFailed",
			task: upsertTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.AddUserMock.Expect(minimock.AnyContext, "user-1").Return(errBoom)
				m.sender.SendErrorMock.Set(func(_ context.Context, msg *model.OutboundError) error {
					assert.Equal(t, "req-upsert", msg.RequestID)
					assert.Contains(t, msg.Error, "boom")
					return nil
				})
				m.repo.MarkFailedMock.Set(func(_ context.Context, requestID string, errMsg string) error {
					assert.Equal(t, "req-upsert", requestID)
					assert.Contains(t, errMsg, "boom")
					return nil
				})
			},
		},
		{
			name: "upsert: BuildCreds fails after AddUser ok",
			task: upsertTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.AddUserMock.Expect(minimock.AnyContext, "user-1").Return(nil)
				m.driver.BuildCredsMock.Expect("user-1").Return("", errBoom)
				m.sender.SendErrorMock.Expect(minimock.AnyContext, &model.OutboundError{
					RequestID: "req-upsert",
					Error:     "build creds: boom",
				}).Return(nil)
				m.repo.MarkFailedMock.Expect(minimock.AnyContext, "req-upsert", "build creds: boom").Return(nil)
			},
		},
		{
			name: "upsert: SendUpsertAck fails",
			task: upsertTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.AddUserMock.Expect(minimock.AnyContext, "user-1").Return(nil)
				m.driver.BuildCredsMock.Expect("user-1").Return("vless://creds", nil)
				m.repo.UpsertUserMock.Expect(minimock.AnyContext, "user-1", "xray").Return(nil)
				m.sender.SendUpsertAckMock.Set(func(_ context.Context, _ *model.OutboundUpsert) error {
					return errBoom
				})
				m.sender.SendErrorMock.Set(func(_ context.Context, msg *model.OutboundError) error {
					assert.Equal(t, "req-upsert", msg.RequestID)
					assert.Contains(t, msg.Error, "send upsert ack")
					return nil
				})
				m.repo.MarkFailedMock.Set(func(_ context.Context, requestID string, errMsg string) error {
					assert.Equal(t, "req-upsert", requestID)
					assert.Contains(t, errMsg, "send upsert ack")
					return nil
				})
			},
		},
		{
			name: "remove: RemoveUser fails",
			task: removeTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.RemoveUserMock.Expect(minimock.AnyContext, "user-2").Return(errBoom)
				m.sender.SendErrorMock.Expect(minimock.AnyContext, &model.OutboundError{
					RequestID: "req-remove",
					Error:     "remove user: boom",
				}).Return(nil)
				m.repo.MarkFailedMock.Expect(minimock.AnyContext, "req-remove", "remove user: boom").Return(nil)
			},
		},
		{
			name: "remove: SendRemoveAck fails",
			task: removeTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.RemoveUserMock.Expect(minimock.AnyContext, "user-2").Return(nil)
				m.repo.DeleteUserMock.Expect(minimock.AnyContext, "user-2", "xray").Return(nil)
				m.sender.SendRemoveAckMock.Set(func(_ context.Context, _ *model.OutboundRemove) error {
					return errBoom
				})
				m.sender.SendErrorMock.Set(func(_ context.Context, msg *model.OutboundError) error {
					assert.Contains(t, msg.Error, "send remove ack")
					return nil
				})
				m.repo.MarkFailedMock.Set(func(_ context.Context, _ string, errMsg string) error {
					assert.Contains(t, errMsg, "send remove ack")
					return nil
				})
			},
		},
		{
			name: "upsert: repo.UpsertUser fails",
			task: upsertTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.AddUserMock.Expect(minimock.AnyContext, "user-1").Return(nil)
				m.driver.BuildCredsMock.Expect("user-1").Return("vless://creds", nil)
				m.repo.UpsertUserMock.Expect(minimock.AnyContext, "user-1", "xray").Return(errBoom)
				m.sender.SendErrorMock.Expect(minimock.AnyContext, &model.OutboundError{
					RequestID: "req-upsert",
					Error:     "repo upsert user: boom",
				}).Return(nil)
				m.repo.MarkFailedMock.Expect(minimock.AnyContext, "req-upsert", "repo upsert user: boom").Return(nil)
			},
		},
		{
			name: "remove: repo.DeleteUser fails",
			task: removeTask,
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.RemoveUserMock.Expect(minimock.AnyContext, "user-2").Return(nil)
				m.repo.DeleteUserMock.Expect(minimock.AnyContext, "user-2", "xray").Return(errBoom)
				m.sender.SendErrorMock.Expect(minimock.AnyContext, &model.OutboundError{
					RequestID: "req-remove",
					Error:     "repo delete user: boom",
				}).Return(nil)
				m.repo.MarkFailedMock.Expect(minimock.AnyContext, "req-remove", "repo delete user: boom").Return(nil)
			},
		},
		{
			name: "unknown task kind",
			task: &model.Task{RequestID: "req-x", UserID: "u", Kind: model.TaskKind("weird")},
			setup: func(t *testing.T, m *workerMocks) {
				m.sender.SendErrorMock.Set(func(_ context.Context, msg *model.OutboundError) error {
					assert.Equal(t, "req-x", msg.RequestID)
					assert.Contains(t, msg.Error, "unknown task kind")
					return nil
				})
				m.repo.MarkFailedMock.Set(func(_ context.Context, requestID string, errMsg string) error {
					assert.Equal(t, "req-x", requestID)
					assert.Contains(t, errMsg, "unknown task kind")
					return nil
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newWorkerMocks(t)
			tc.setup(t, m)
			w := newTestWorker(m, "agent-X")
			w.processTask(ctx, tc.task)
		})
	}
}

func TestTickTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errBoom := errors.New("pull boom")

	tests := []struct {
		name  string
		setup func(t *testing.T, m *workerMocks)
	}{
		{
			name: "empty pull -> no driver/sender calls",
			setup: func(t *testing.T, m *workerMocks) {
				m.repo.PullPendingTasksMock.Expect(minimock.AnyContext, 32).Return(nil, nil)
			},
		},
		{
			name: "pull error -> swallowed, no further calls",
			setup: func(t *testing.T, m *workerMocks) {
				m.repo.PullPendingTasksMock.Expect(minimock.AnyContext, 32).Return(nil, errBoom)
			},
		},
		{
			name: "batch with one failing and one succeeding",
			setup: func(t *testing.T, m *workerMocks) {
				m.repo.PullPendingTasksMock.Expect(minimock.AnyContext, 32).Return([]*model.Task{
					{RequestID: "ok", UserID: "u-ok", DriverType: "xray", Kind: model.TaskRemove},
					{RequestID: "fail", UserID: "u-fail", DriverType: "xray", Kind: model.TaskRemove},
				}, nil)

				m.driver.RemoveUserMock.When(minimock.AnyContext, "u-ok").Then(nil)
				m.driver.RemoveUserMock.When(minimock.AnyContext, "u-fail").Then(errors.New("nope"))

				m.repo.DeleteUserMock.Expect(minimock.AnyContext, "u-ok", "xray").Return(nil)
				m.sender.SendRemoveAckMock.Expect(minimock.AnyContext, &model.OutboundRemove{RequestID: "ok"}).Return(nil)
				m.repo.MarkDoneMock.Expect(minimock.AnyContext, "ok").Return(nil)

				m.sender.SendErrorMock.Set(func(_ context.Context, msg *model.OutboundError) error {
					assert.Equal(t, "fail", msg.RequestID)
					return nil
				})
				m.repo.MarkFailedMock.Set(func(_ context.Context, requestID string, _ string) error {
					assert.Equal(t, "fail", requestID)
					return nil
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newWorkerMocks(t)
			tc.setup(t, m)
			w := newTestWorker(m, "agent-X")
			w.tickTask(ctx)
		})
	}
}

func TestTickStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errBoom := errors.New("boom")

	tests := []struct {
		name  string
		setup func(t *testing.T, m *workerMocks)
	}{
		{
			name: "happy path: forwards cumulative usage",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.CollectStatsMock.Expect(minimock.AnyContext).Return([]*model.UserUsage{
					{UserID: "u1", BytesUp: 100, BytesDown: 200, IPCount: 1},
					{UserID: "u2", BytesUp: 300, BytesDown: 400, IPCount: 2},
				}, nil)
				m.sender.SendStatsMock.Set(func(_ context.Context, msg *model.OutboundStats) error {
					assert.Equal(t, "agent-X", msg.AgentID)
					assert.WithinDuration(t, time.Now(), msg.WindowEnd, time.Second)
					require.Len(t, msg.Users, 2)
					assert.Equal(t, model.UserUsage{UserID: "u1", BytesUp: 100, BytesDown: 200, IPCount: 1}, msg.Users[0])
					assert.Equal(t, model.UserUsage{UserID: "u2", BytesUp: 300, BytesDown: 400, IPCount: 2}, msg.Users[1])
					return nil
				})
			},
		},
		{
			name: "empty stats still sends heartbeat with empty Users",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.CollectStatsMock.Expect(minimock.AnyContext).Return(nil, nil)
				m.sender.SendStatsMock.Set(func(_ context.Context, msg *model.OutboundStats) error {
					assert.Empty(t, msg.Users)
					assert.Equal(t, "agent-X", msg.AgentID)
					return nil
				})
			},
		},
		{
			name: "CollectStats fails -> SendStats not called",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.CollectStatsMock.Expect(minimock.AnyContext).Return(nil, errBoom)
			},
		},
		{
			name: "SendStats error is swallowed",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.CollectStatsMock.Expect(minimock.AnyContext).Return([]*model.UserUsage{
					{UserID: "u1", BytesUp: 1},
				}, nil)
				m.sender.SendStatsMock.Set(func(_ context.Context, _ *model.OutboundStats) error {
					return errBoom
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newWorkerMocks(t)
			tc.setup(t, m)
			w := newTestWorker(m, "agent-X")
			w.tickStats(ctx)
		})
	}
}

func TestTickSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errBoom := errors.New("boom")

	tests := []struct {
		name           string
		setup          func(t *testing.T, m *workerMocks)
		expectAdds     []string
		expectRemoves  []string
		addErrorsForID map[string]error
	}{
		{
			name: "diff: A,B in xray; A,C in db -> add C, remove B",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return([]string{"A", "B"}, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return([]string{"A", "C"}, nil)
			},
			expectAdds:    []string{"C"},
			expectRemoves: []string{"B"},
		},
		{
			name: "empty db, non-empty xray -> remove all from xray (trust db)",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return([]string{"A", "B"}, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return(nil, nil)
			},
			expectAdds:    nil,
			expectRemoves: []string{"A", "B"},
		},
		{
			name: "empty xray, non-empty db -> add all",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return(nil, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return([]string{"A"}, nil)
			},
			expectAdds:    []string{"A"},
			expectRemoves: nil,
		},
		{
			name: "in_xray == in_db -> no add/remove",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return([]string{"A", "B"}, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return([]string{"A", "B"}, nil)
			},
		},
		{
			name: "ListUsers err -> repo not called, no Add/Remove",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return(nil, errBoom)
			},
		},
		{
			name: "ListUserIDs err -> no Add/Remove",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return([]string{"A"}, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return(nil, errBoom)
			},
		},
		{
			name: "AddUser failure on one id does not block others",
			setup: func(t *testing.T, m *workerMocks) {
				m.driver.ListUsersMock.Expect(minimock.AnyContext).Return(nil, nil)
				m.repo.ListUserIDsMock.Expect(minimock.AnyContext).Return([]string{"A", "B"}, nil)
			},
			expectAdds:     []string{"A", "B"},
			addErrorsForID: map[string]error{"A": errBoom},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newWorkerMocks(t)
			tc.setup(t, m)

			var addedIDs, removedIDs []string

			if len(tc.expectAdds) > 0 {
				m.driver.AddUserMock.Set(func(_ context.Context, userID string) error {
					addedIDs = append(addedIDs, userID)
					if e, ok := tc.addErrorsForID[userID]; ok {
						return e
					}
					return nil
				})
			}
			if len(tc.expectRemoves) > 0 {
				m.driver.RemoveUserMock.Set(func(_ context.Context, userID string) error {
					removedIDs = append(removedIDs, userID)
					return nil
				})
			}

			w := newTestWorker(m, "agent-X")
			w.tickSync(ctx)

			assert.ElementsMatch(t, tc.expectAdds, addedIDs, "added ids mismatch")
			assert.ElementsMatch(t, tc.expectRemoves, removedIDs, "removed ids mismatch")
		})
	}
}

func TestDiffUserSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		inDB       []string
		inXray     []string
		wantAdd    []string
		wantRemove []string
	}{
		{name: "intersect", inDB: []string{"A", "C"}, inXray: []string{"A", "B"}, wantAdd: []string{"C"}, wantRemove: []string{"B"}},
		{name: "empty db", inDB: nil, inXray: []string{"A", "B"}, wantAdd: nil, wantRemove: []string{"A", "B"}},
		{name: "empty xray", inDB: []string{"A"}, inXray: nil, wantAdd: []string{"A"}, wantRemove: nil},
		{name: "identical", inDB: []string{"A", "B"}, inXray: []string{"B", "A"}, wantAdd: nil, wantRemove: nil},
		{name: "both empty", inDB: nil, inXray: nil, wantAdd: nil, wantRemove: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			add, remove := diffUserSets(tc.inDB, tc.inXray)
			sort.Strings(add)
			sort.Strings(remove)
			assert.Equal(t, tc.wantAdd, add)
			assert.Equal(t, tc.wantRemove, remove)
		})
	}
}

func TestWorker_Run_PropagatesCtxCancel(t *testing.T) {
	t.Parallel()

	m := newWorkerMocks(t)

	// Stub everything so loops can tick freely; Run должен завершиться по ctx, не по ошибке внутри тиков.
	m.repo.PullPendingTasksMock.Return(nil, nil)
	m.driver.CollectStatsMock.Return(nil, nil)
	m.sender.SendStatsMock.Return(nil)
	m.driver.ListUsersMock.Return(nil, nil)
	m.repo.ListUserIDsMock.Return(nil, nil)

	cfg := WorkerConfig{
		TaskPollInterval: 10 * time.Millisecond,
		StatsInterval:    10 * time.Millisecond,
		SyncInterval:     10 * time.Millisecond,
		TaskBatchLimit:   32,
		AgentID:          "agent-X",
	}
	w := NewWorker(cfg, m.repo, m.driver, m.sender, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
