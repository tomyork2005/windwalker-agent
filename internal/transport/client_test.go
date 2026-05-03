package transport

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/config"
	"agent/internal/model"
	"agent/internal/transport/mocks"

	"github.com/gojuno/minimock/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// Тестовый сервер: assert hello → send welcome → send one upsert task →
// складывает все Response'ы в канал. Дальше держит стрим живым, пока клиент не закроет.
type testControlPlane struct {
	controlpb.UnimplementedControlPlaneServer

	gotHello     chan *controlpb.AgentHello
	gotResponses chan *controlpb.Response
	pushTask     *controlpb.Task
	welcomeAgent string
}

func (s *testControlPlane) Workstream(stream controlpb.ControlPlane_WorkstreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello, ok := first.GetMsg().(*controlpb.AgentToControl_Hello)
	if !ok {
		return io.EOF
	}
	s.gotHello <- hello.Hello

	if err := stream.Send(&controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Welcome{
		Welcome: &controlpb.Welcome{AgentId: s.welcomeAgent, Message: "ok"},
	}}); err != nil {
		return err
	}

	if err := stream.Send(&controlpb.ControlToAgent{Message: &controlpb.ControlToAgent_Task{
		Task: s.pushTask,
	}}); err != nil {
		return err
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if resp, ok := msg.GetMsg().(*controlpb.AgentToControl_Resp); ok {
			s.gotResponses <- resp.Resp
		}
	}
}

func TestClientRunHappyPath(t *testing.T) {
	t.Parallel()

	const bufSize = 1 << 16
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()

	pushTask := &controlpb.Task{
		RequestId: "t1",
		Body: &controlpb.Task_Upsert{Upsert: &controlpb.UserUpsertRequest{
			User: &controlpb.User{UserId: "u1", DriverType: "xray"},
		}},
	}
	tcp := &testControlPlane{
		gotHello:     make(chan *controlpb.AgentHello, 1),
		gotResponses: make(chan *controlpb.Response, 4),
		pushTask:     pushTask,
		welcomeAgent: "from-server",
	}
	controlpb.RegisterControlPlaneServer(srv, tcp)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// Клиент: подменяем dial через bufconn, чтобы Run не пытался ходить в сеть.
	cfg := config.TransportGrpcConfig{
		Address:       "bufnet",
		AgentID:       "a-cfg",
		InstanceID:    "i-1",
		Region:        "eu",
		SendQueueSize: 8,
		ReconnectMin:  50 * time.Millisecond,
		ReconnectMax:  200 * time.Millisecond,
		DialTimeout:   time.Second,
	}
	c := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	c.conn = conn
	c.api = controlpb.NewControlPlaneClient(conn)

	// Service-мок: на UpsertUser синхронно ack'аем через Sender.
	mc := minimock.NewController(t)
	svc := mocks.NewServiceMock(mc)
	svc.UpsertUserMock.Set(func(ctx context.Context, requestID string, user *model.User) error {
		assert.Equal(t, "t1", requestID)
		assert.Equal(t, &model.User{ID: "u1", DriverType: "xray"}, user)
		return c.SendUpsertAck(ctx, &model.OutboundUpsert{
			RequestID:   "t1",
			UserID:      "u1",
			AgentID:     "a-cfg",
			DriverType:  "xray",
			VlessURI:    "vless://x",
			GeneratedAt: time.Unix(1700000000, 0).UTC(),
		})
	})

	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx, svc) }()

	// Сервер должен получить hello.
	select {
	case h := <-tcp.gotHello:
		assert.Equal(t, "a-cfg", h.GetAgentId())
		assert.Equal(t, "i-1", h.GetInstanceId())
		assert.Equal(t, "eu", h.GetRegion())
		assert.Equal(t, []string{"xray"}, h.GetDriverTypes())
	case <-time.After(2 * time.Second):
		t.Fatal("hello not received by server")
	}

	// И ответ на пушнутую таску.
	want := &controlpb.Response{
		RequestId: "t1",
		Body: &controlpb.Response_Upsert{Upsert: &controlpb.UserUpsertResponse{
			Creds: &controlpb.UserCreds{
				UserId:     "u1",
				AgentId:    "a-cfg",
				DriverType: "xray",
				GeneratedAt: upsertAckToProto(&model.OutboundUpsert{
					GeneratedAt: time.Unix(1700000000, 0).UTC(),
				}).GetResp().GetUpsert().GetCreds().GetGeneratedAt(),
				Config: &controlpb.UserCreds_Vless{Vless: &controlpb.VlessCreds{Uri: "vless://x"}},
			},
		}},
	}
	select {
	case got := <-tcp.gotResponses:
		assert.Truef(t, proto.Equal(want, got), "want=%v got=%v", want, got)
	case <-time.After(2 * time.Second):
		t.Fatal("response not received by server")
	}

	cancel()

	select {
	case err := <-runErr:
		// Run возвращает либо ctx.Err() (cancelled/deadline), либо nil — обе годятся.
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
