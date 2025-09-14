package drivers

import (
	"agent/internal"
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"

	proxycmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
)

var _ internal.VPNDriver = (*XrayDriver)(nil)

type XrayOptions struct {
	ServiceName string
	APIAddr     string
	InboundTag  string
	Protocol    string
	GRPCCreds   credentials.TransportCredentials

	DialTimeout time.Duration
	OpTimeout   time.Duration
}

type XrayDriver struct {
	name string
	cfg  XrayOptions

	mu    sync.Mutex
	conn  *grpc.ClientConn
	h     proxycmd.HandlerServiceClient
	stats statscmd.StatsServiceClient
}

func NewXrayDriver(options XrayOptions) *XrayDriver {
	if options.DialTimeout == 0 {
		options.DialTimeout = time.Second * 5
	}
	if options.OpTimeout == 0 {
		options.OpTimeout = time.Second * 3
	}
	return &XrayDriver{
		name: "xray",
		cfg:  options,
	}
}

// NetworkDriver

func (d *XrayDriver) Name() string {
	return d.name
}

func (d *XrayDriver) Start(ctx context.Context) error {
	return d.systemctl(ctx, "start")
}

func (d *XrayDriver) Stop(ctx context.Context) error {
	return d.systemctl(ctx, "stop")
}

func (d *XrayDriver) Restart(ctx context.Context) error {
	return d.systemctl(ctx, "restart")
}

func (d *XrayDriver) Health(ctx context.Context) error {
	if err := d.systemctl(ctx, "is-active"); err != nil {
		return fmt.Errorf("xray not active: %w", err)
	}
	if err := d.ensureConn(ctx); err != nil {
		return err
	}

	// api answering for our requests?
	ctx, cancel := context.WithTimeout(ctx, d.cfg.OpTimeout)
	defer cancel()

	name := fmt.Sprintf("inbound>>>%s>>>traffic>>>downlink", d.cfg.InboundTag)
	_, err := d.stats.GetStats(ctx, &statscmd.GetStatsRequest{
		Name:   name,
		Reset_: false,
	})
	if err == nil {
		return nil
	}

	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.NotFound:
			return nil
		case codes.Unimplemented:
			return nil
		default:
			return fmt.Errorf("xray grpc unhealthy (%s): %w", st.Code(), err)
		}
	}

	return fmt.Errorf("xray grpc unhealthy: %w", err)
}

// Helpers
func (d *XrayDriver) systemctl(ctx context.Context, action string) error {
	if d.cfg.ServiceName == "" {
		d.cfg.ServiceName = "xray"
	}
	cmd := exec.CommandContext(ctx, "systemctl", action, d.cfg.ServiceName)
	if action == "is-active" {
		return cmd.Run()
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s failed: %v: %s", action, d.cfg.ServiceName, err, string(out))
	}
	return nil
}

func (d *XrayDriver) ensureConn(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conn != nil {
		return nil
	}

	var opts []grpc.DialOption
	if d.cfg.GRPCCreds != nil {
		opts = append(opts, grpc.WithTransportCredentials(d.cfg.GRPCCreds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(d.cfg.APIAddr, opts...)
	if err != nil {
		return fmt.Errorf("xray grpc client failed: %w", err)
	}

	d.conn = conn
	d.h = proxycmd.NewHandlerServiceClient(conn)
	d.stats = statscmd.NewStatsServiceClient(conn)
	return nil
}
