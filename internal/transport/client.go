package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	controlpb "agent/api/control"
	"agent/internal/config"
	"agent/internal/model"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultSendQueueSize = 128

type Service interface {
	UpsertUser(ctx context.Context, requestID string, user *model.User) error
	DeleteTask(ctx context.Context, requestID string, user *model.User) error
}

type Client struct {
	cfg   config.TransportGrpcConfig
	log   *slog.Logger
	sendQ chan *controlpb.AgentToControl

	conn *grpc.ClientConn
	api  controlpb.ControlPlaneClient
}

func New(cfg config.TransportGrpcConfig, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}

	q := cfg.SendQueueSize
	if q <= 0 {
		q = defaultSendQueueSize
	}

	return &Client{
		cfg:   cfg,
		log:   log.With("component", "transport"),
		sendQ: make(chan *controlpb.AgentToControl, q),
	}
}

func (c *Client) Run(ctx context.Context, svc Service) error {
	defer c.closeConn()

	back := c.cfg.ReconnectMin
	for {
		err := c.runSession(ctx, svc)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.log.Warn("session ended, reconnecting", "err", err, "backoff", back)
		select {
		case <-time.After(back):
		case <-ctx.Done():
			return ctx.Err()
		}

		back = nextBackoff(back, c.cfg.ReconnectMax)
	}
}

func (c *Client) runSession(ctx context.Context, svc Service) error {
	if err := c.connect(ctx); err != nil {
		return err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := c.api.Workstream(sessCtx)
	if err != nil {
		return fmt.Errorf("open workstream: %w", err)
	}

	if err := c.sendHello(stream); err != nil {
		return err
	}
	if err := c.recvWelcome(stream); err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(sessCtx)
	g.Go(func() error { return c.writer(gctx, stream) })
	g.Go(func() error { return c.reader(gctx, stream, svc) })
	return g.Wait()
}

func (c *Client) connect(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, c.cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.cfg.Address, err)
	}

	c.conn = conn
	c.api = controlpb.NewControlPlaneClient(conn)
	c.log.Info("dialed", "address", c.cfg.Address)

	return nil
}

func (c *Client) closeConn() {
	if c.conn == nil {
		return
	}

	if err := c.conn.Close(); err != nil {
		c.log.Warn("close conn", "err", err)
	}
	c.conn = nil
	c.api = nil
}

func (c *Client) sendHello(stream controlpb.ControlPlane_WorkstreamClient) error {
	hello := &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Hello{Hello: &controlpb.AgentHello{
		AgentId:     c.cfg.AgentID,
		InstanceId:  c.cfg.InstanceID,
		Region:      c.cfg.Region,
		DriverTypes: []string{"xray"},
	}}}
	if err := stream.Send(hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	c.log.Info("hello sent", "agent_id", c.cfg.AgentID, "instance_id", c.cfg.InstanceID)

	return nil
}

func (c *Client) recvWelcome(stream controlpb.ControlPlane_WorkstreamClient) error {
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv welcome: %w", err)
	}

	w, ok := msg.GetMessage().(*controlpb.ControlToAgent_Welcome)
	if !ok {
		return fmt.Errorf("expected welcome, got %T", msg.GetMessage())
	}
	c.log.Info("welcome received", "agent_id", w.Welcome.GetAgentId(), "message", w.Welcome.GetMessage())

	return nil
}

func (c *Client) writer(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient) error {
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		case m := <-c.sendQ:
			if err := stream.Send(m); err != nil {
				return fmt.Errorf("stream send: %w", err)
			}
		}
	}
}

func (c *Client) reader(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient, svc Service) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return fmt.Errorf("stream recv: %w", err)
		}

		c.handleControlMessage(ctx, msg, svc)
	}
}

func (c *Client) handleControlMessage(ctx context.Context, m *controlpb.ControlToAgent, svc Service) {
	switch body := m.GetMessage().(type) {
	case *controlpb.ControlToAgent_Task:
		c.handleTask(ctx, body.Task, svc)
	default:
		c.log.Warn("unknown control message", "type", fmt.Sprintf("%T", body))
	}
}

func (c *Client) handleTask(ctx context.Context, t *controlpb.Task, svc Service) {
	reqID := t.GetRequestId()
	c.log.Info("task received", "request_id", reqID)

	var err error
	switch body := t.GetBody().(type) {
	case *controlpb.Task_Upsert:
		err = svc.UpsertUser(ctx, reqID, userFromProto(body.Upsert.GetUser()))
	case *controlpb.Task_Remove:
		err = svc.DeleteTask(ctx, reqID, userFromProto(body.Remove.GetUser()))
	default:
		err = errors.New("empty task body")
	}
	if err == nil {
		return
	}

	c.log.Warn("task handling failed", "request_id", reqID, "err", err)
	if sendErr := c.SendError(ctx, &model.OutboundError{
		RequestID: reqID,
		Error:     err.Error(),
	}); sendErr != nil {
		c.log.Error("enqueue error response", "request_id", reqID, "err", sendErr)
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	if cur <= 0 {
		return max
	}
	next := cur * 2
	if next > max {
		return max
	}
	return next
}
