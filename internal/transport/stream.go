package transport

import (
	"context"
	"errors"
	"fmt"

	controlpb "agent/api/control"
	"agent/internal/config"
	"agent/internal/logx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"time"
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
		return err
	}

	c.client = client
	c.api = controlpb.NewControlPlaneClient(c.client)
	return nil
}

func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

func (c *Client) Run(ctx context.Context, h TaskHandlers) error {
	back := c.cfg.ReconnectMin

	for {
		if err := c.runOnce(ctx, h); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			select {
			case <-time.After(back):
			case <-ctx.Done():
				return ctx.Err()
			}
			back *= 2
			if back > c.cfg.ReconnectMax {
				back = c.cfg.ReconnectMax
			}
			continue
		}
		back = c.cfg.ReconnectMin
	}
}

func (c *Client) runOnce(ctx context.Context, h TaskHandlers) error {
	if c.client == nil {
		if err := c.connect(); err != nil {
			return err
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Starting bidi-stream
	stream, err := c.api.Workstream(streamCtx)
	if err != nil {
		return err
	}

	if err := stream.Send(&controlpb.AgentToControl{
		Msg: &controlpb.AgentToControl_Hello{
			Hello: &controlpb.AgentHello{
				InstanceId:  c.cfg.InstanceID,
				Region:      c.cfg.Region,
				Version:     c.cfg.Version,
				DriverTypes: c.cfg.DriverTypes,
			},
		},
	}); err != nil {
		return err
	}

	// starting 2 workers
	errCh := make(chan error, 2)
	go func() { errCh <- c.writer(streamCtx, stream) }()
	go func() { errCh <- c.reader(streamCtx, stream, h) }()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Client) writer(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient) error {
	hb := time.NewTicker(c.cfg.HeartbeatPeriod)
	defer hb.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-c.sendQ:
			if err := stream.Send(m); err != nil {
				return err
			}
		case <-hb.C:
			if err := stream.Send(&controlpb.AgentToControl{
				Msg: &controlpb.AgentToControl_Heartbeat{
					Heartbeat: &controlpb.Heartbeat{
						AgentId:       c.cfg.AgentID,
						UptimeSeconds: uint32(time.Since(c.start) / time.Second),
					},
				},
			}); err != nil {
				return err
			}
		}
	}
}

func (c *Client) reader(ctx context.Context, stream controlpb.ControlPlane_WorkstreamClient, h TaskHandlers) error {
	for {
		in, err := stream.Recv()
		if err != nil {
			return err
		} // network/server close --> stop run

		switch m := in.Msg.(type) {
		case *controlpb.ControlToAgent_Welcome:
			if m.Welcome.GetAgentId() != "" {
				c.cfg.AgentID = m.Welcome.GetAgentId()
				aid := m.Welcome.GetAgentId()
				logx.Set("agent_id", aid)
				logx.Info("welcome received", "agent_id", aid)
			}
		case *controlpb.ControlToAgent_Task:
			if err := RouteTask(ctx, h, m.Task, c.Send); err != nil {
				logx.Error(fmt.Sprintf("Error on route task seg - %v, : %v", m.Task.GetMeta(), err))
			}
		}
	}
}

func (c *Client) Send(m *controlpb.AgentToControl) error {
	select {
	case c.sendQ <- m:
		return nil
	default:
		c.sendQ <- m
		return nil
	}
}
