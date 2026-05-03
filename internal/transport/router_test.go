package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/model"
	"agent/internal/transport/mocks"

	"github.com/gojuno/minimock/v3"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

type routerMocks struct {
	svc *mocks.ServiceMock
}

func newRouterMocks(t *testing.T) *routerMocks {
	t.Helper()
	mc := minimock.NewController(t)
	return &routerMocks{svc: mocks.NewServiceMock(mc)}
}

func newRouterClient(t *testing.T, queue int) *Client {
	t.Helper()
	return &Client{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sendQ: make(chan *controlpb.AgentToControl, queue),
	}
}

func taskUpsert(reqID, userID, driverType string) *controlpb.ControlToAgent {
	return &controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Task{Task: &controlpb.Task{
		RequestId: reqID,
		Body: &controlpb.Task_Upsert{Upsert: &controlpb.UserUpsertRequest{
			User: &controlpb.User{UserId: userID, DriverType: driverType},
		}},
	}}}
}

func taskRemove(reqID, userID, driverType string) *controlpb.ControlToAgent {
	return &controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Task{Task: &controlpb.Task{
		RequestId: reqID,
		Body: &controlpb.Task_Remove{Remove: &controlpb.UserRemoveRequest{
			User: &controlpb.User{UserId: userID, DriverType: driverType},
		}},
	}}}
}

func taskEmptyBody(reqID string) *controlpb.ControlToAgent {
	return &controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Task{Task: &controlpb.Task{
		RequestId: reqID,
	}}}
}

func TestHandleControlMessage(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name       string
		input      *controlpb.ControlToAgent
		setup      func(t *testing.T, m *routerMocks)
		wantQueued []*controlpb.AgentToControl
	}{
		{
			name:  "task upsert -> Service.UpsertUser",
			input: taskUpsert("r1", "u1", "xray"),
			setup: func(t *testing.T, m *routerMocks) {
				m.svc.UpsertUserMock.Expect(
					minimock.AnyContext, "r1", &model.User{ID: "u1", DriverType: "xray"},
				).Return(nil)
			},
		},
		{
			name:  "task remove -> Service.DeleteTask",
			input: taskRemove("r2", "u2", "xray"),
			setup: func(t *testing.T, m *routerMocks) {
				m.svc.DeleteTaskMock.Expect(
					minimock.AnyContext, "r2", &model.User{ID: "u2", DriverType: "xray"},
				).Return(nil)
			},
		},
		{
			name:  "empty task body -> error response enqueued",
			input: taskEmptyBody("r3"),
			setup: func(t *testing.T, m *routerMocks) {},
			wantQueued: []*controlpb.AgentToControl{
				errorToProto(&model.OutboundError{RequestID: "r3", Error: "empty task body"}),
			},
		},
		{
			name:  "Service.UpsertUser error -> error response enqueued",
			input: taskUpsert("r1", "u1", "xray"),
			setup: func(t *testing.T, m *routerMocks) {
				m.svc.UpsertUserMock.Expect(
					minimock.AnyContext, "r1", &model.User{ID: "u1", DriverType: "xray"},
				).Return(errBoom)
			},
			wantQueued: []*controlpb.AgentToControl{
				errorToProto(&model.OutboundError{RequestID: "r1", Error: "boom"}),
			},
		},
		{
			name:  "Service.DeleteTask error -> error response enqueued",
			input: taskRemove("r2", "u2", "xray"),
			setup: func(t *testing.T, m *routerMocks) {
				m.svc.DeleteTaskMock.Expect(
					minimock.AnyContext, "r2", &model.User{ID: "u2", DriverType: "xray"},
				).Return(errBoom)
			},
			wantQueued: []*controlpb.AgentToControl{
				errorToProto(&model.OutboundError{RequestID: "r2", Error: "boom"}),
			},
		},
		{
			name: "unexpected Welcome mid-session -> default branch, nothing enqueued",
			input: &controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Welcome{
				Welcome: &controlpb.Welcome{AgentId: "x"},
			}},
			setup: func(t *testing.T, m *routerMocks) {},
		},
		{
			name:  "unknown oneof (nil msg) -> default branch, nothing enqueued",
			input: &controlpb.ControlToAgent{},
			setup: func(t *testing.T, m *routerMocks) {},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newRouterMocks(t)
			tc.setup(t, m)

			c := newRouterClient(t, 8)
			c.handleControlMessage(context.Background(), tc.input, m.svc)

			got := drainAll(c)
			assertProtoSliceEqual(t, tc.wantQueued, got)
		})
	}
}

func drainAll(c *Client) []*controlpb.AgentToControl {
	var out []*controlpb.AgentToControl
	for {
		select {
		case m := <-c.sendQ:
			out = append(out, m)
		case <-time.After(20 * time.Millisecond):
			return out
		}
	}
}

func assertProtoSliceEqual(t *testing.T, want, got []*controlpb.AgentToControl) {
	t.Helper()
	if !assert.Len(t, got, len(want)) {
		return
	}
	for i := range want {
		assert.Truef(t, proto.Equal(want[i], got[i]),
			"msg %d: want=%v got=%v", i, want[i], got[i])
	}
}
