package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	controlpb "agent/api/control"
	"agent/internal/config"
	"agent/internal/logx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Client struct {
	cfg    config.TransportGrpcConfig
	client *grpc.ClientConn
	api    controlpb.ControlPlaneClient

	sendQ chan *controlpb.AgentToControl
	start time.Time
}

func NewClient(cfg config.TransportGrpcConfig) *Client {
	if cfg.HeartbeatPeriod == 0 {
		cfg.HeartbeatPeriod = 20 * time.Second
	}
	if cfg.SendQueueSize == 0 {
		cfg.SendQueueSize = 128
	}
	if cfg.ReconnectMin == 0 {
		cfg.ReconnectMin = 500 * time.Millisecond
	}
	if cfg.ReconnectMax == 0 {
		cfg.ReconnectMax = 10 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}

	return &Client{
		cfg:   cfg,
		sendQ: make(chan *controlpb.AgentToControl, cfg.SendQueueSize),
		start: time.Now(),
	}
}

func (c *Client) connect() error {
	logx.Info("transport connect started",
		"addr", c.cfg.Address,
		"dial_timeout", c.cfg.DialTimeout.String(),
		"reconnect_min", c.cfg.ReconnectMin.String(),
		"reconnect_max", c.cfg.ReconnectMax.String(),
	)

	ka := keepalive.ClientParameters{
		Time:                25 * time.Second,
		Timeout:             5 * time.Second,
		PermitWithoutStream: true,
	}
	bo := backoff.Config{BaseDelay: 200 * time.Millisecond, Multiplier: 1.6, MaxDelay: 5 * time.Second}

	client, err := grpc.NewClient(
		c.cfg.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()), // TODO: mTLS
		grpc.WithKeepaliveParams(ka),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           bo,
			MinConnectTimeout: 3 * time.Second,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(64<<20),
			grpc.MaxCallSendMsgSize(64<<20),
		),
	)
	if err != nil {
		logx.Error("transport connect failed",
			"addr", c.cfg.Address,
			"err", err,
		)
		return err
	}

	c.client = client
	c.api = controlpb.NewControlPlaneClient(c.client)

	logx.Info("transport connect client created",
		"addr", c.cfg.Address,
	)

	return nil
}

func (c *Client) Close() error {
	if c.client != nil {
		logx.Info("transport closing grpc client")
		return c.client.Close()
	}
	return nil
}

func (c *Client) Run(ctx context.Context, h TaskHandlers) error {
	back := c.cfg.ReconnectMin

	logx.Info("transport run started",
		"addr", c.cfg.Address,
		"heartbeat_period", c.cfg.HeartbeatPeriod.String(),
		"send_queue_size", c.cfg.SendQueueSize,
	)

	for {
		if err := c.runOnce(ctx, h); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logx.Info("transport stopped by context", "err", err)
				return err
			}

			logx.Error("transport runOnce failed",
				"addr", c.cfg.Address,
				"err", err,
				"retry_in", back.String(),
			)

			select {
			case <-time.After(back):
			case <-ctx.Done():
				logx.Info("transport stopped while waiting reconnect", "err", ctx.Err())
				return ctx.Err()
			}

			back *= 2
			if back > c.cfg.ReconnectMax {
				back = c.cfg.ReconnectMax
			}
			continue
		}

		logx.Info("transport runOnce finished without error, reset backoff")
		back = c.cfg.ReconnectMin
	}
}

func (c *Client) runOnce(ctx context.Context, h TaskHandlers) error {
	logx.Info("transport runOnce started",
		"addr", c.cfg.Address,
		"agent_id", c.cfg.AgentID,
		"instance_id", c.cfg.InstanceID,
		"region", c.cfg.Region,
		"version", c.cfg.Version,
		"driver_types", c.cfg.DriverTypes,
	)

	if c.client == nil {
		logx.Info("transport grpc client is nil, connecting")
		if err := c.connect(); err != nil {
			return err
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logx.Info("transport opening workstream")
	stream, err := c.api.Workstream(streamCtx)
	if err != nil {
		logx.Error("transport workstream open failed", "err", err)
		return err
	}
	logx.Info("transport workstream opened")

	logx.Info("transport sending hello",
		"instance_id", c.cfg.InstanceID,
		"region", c.cfg.Region,
		"version", c.cfg.Version,
		"driver_types", c.cfg.DriverTypes,
	)

	if err := stream.Send(&controlpb.AgentToControl{
		Msg: &controlpb.AgentToControl_Hello{
			Hello: &controlpb.AgentHello{
				AgentId:     c.cfg.AgentID,
				InstanceId:  c.cfg.InstanceID,
				Region:      c.cfg.Region,
				Version:     c.cfg.Version,
				DriverTypes: c.cfg.DriverTypes,
			},
		},
	}); err != nil {
		logx.Error("transport hello send failed", "err", err)
		return err
	}
	logx.Info("transport hello sent")

	errCh := make(chan error, 2)

	go func() {
		err := c.writer(streamCtx, stream)
		if err != nil {
			logx.Error("transport writer exited", "err", err)
		} else {
			logx.Info("transport writer exited without error")
		}
		errCh <- err
	}()

	go func() {
		err := c.reader(streamCtx, stream, h)
		if err != nil {
			logx.Error("transport reader exited", "err", err)
		} else {
			logx.Info("transport reader exited without error")
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		logx.Info("transport runOnce context done", "err", ctx.Err())
		return ctx.Err()
	case err := <-errCh:
		logx.Error("transport runOnce received worker error", "err", err)
		return err
	}
}

func (c *Client) writer(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient) error {
	hb := time.NewTicker(c.cfg.HeartbeatPeriod)
	defer hb.Stop()

	logx.Info("transport writer started",
		"heartbeat_period", c.cfg.HeartbeatPeriod.String(),
	)

	for {
		select {
		case <-ctx.Done():
			logx.Info("transport writer context done", "err", ctx.Err())
			return ctx.Err()

		case m := <-c.sendQ:
			logx.Info("transport writer sending queued message")
			if err := stream.Send(m); err != nil {
				logx.Error("transport writer queued send failed", "err", err)
				return err
			}
			logx.Info("transport writer queued message sent")

		case <-hb.C:
			logx.Info("transport writer sending heartbeat",
				"agent_id", c.cfg.AgentID,
				"uptime_seconds", uint32(time.Since(c.start)/time.Second),
			)
			if err := stream.Send(&controlpb.AgentToControl{
				Msg: &controlpb.AgentToControl_Hb{
					Hb: &controlpb.Heartbeat{
						AgentId:       c.cfg.AgentID,
						UptimeSeconds: uint32(time.Since(c.start) / time.Second),
					},
				},
			}); err != nil {
				logx.Error("transport heartbeat send failed", "err", err)
				return err
			}
			logx.Info("transport heartbeat sent")
		}
	}
}

func (c *Client) reader(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient, h TaskHandlers) error {
	logx.Info("transport reader started")

	for {
		in, err := stream.Recv()
		if err != nil {
			logx.Error("transport reader recv failed", "err", err)
			return err
		}

		logx.Info("transport reader received message", "msg_type", fmt.Sprintf("%T", in.Msg))

		switch m := in.Msg.(type) {
		case *controlpb.ControlToAgent_Welcome:
			if aid := m.Welcome.GetAgentId(); aid != "" {
				c.cfg.AgentID = aid
				logx.Set("agent_id", aid)
				if s, ok := h.(interface{ SetAgentID(string) }); ok {
					s.SetAgentID(aid)
				}
				logx.Info("welcome received", "agent_id", aid)
			} else {
				logx.Info("welcome received with empty agent_id")
			}

		case *controlpb.ControlToAgent_Task:
			logx.Info("transport reader received task")
			if err := RouteTask(ctx, h, m.Task, c.Send); err != nil {
				logx.Error(fmt.Sprintf("Error on route task seg - %v, : %v", m.Task.GetMeta(), err))
			}

		default:
			logx.Info("transport reader received unknown message", "msg_type", fmt.Sprintf("%T", in.Msg))
		}
	}
}

func (c *Client) Send(m *controlpb.AgentToControl) error {
	select {
	case c.sendQ <- m:
		logx.Info("transport enqueue message success")
		return nil
	default:
		logx.Info("transport enqueue message blocking because queue is full", "queue_cap", cap(c.sendQ))
		c.sendQ <- m
		logx.Info("transport enqueue message success after blocking")
		return nil
	}
}
