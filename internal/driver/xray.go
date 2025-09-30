package driver

// Driver for Xray, Xray gRPC API working with user`s email, here user`s email == user`s uuid

import (
	"agent/internal/config"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"agent/internal/domain"

	proxycmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var _ Driver = (*XrayDriver)(nil)

const (
	vlessProtocol = "vless"
	vmessProtocol = "vmess"
)

type XrayDriver struct {
	name string
	cfg  config.XrayConfig

	mu   sync.Mutex
	conn *grpc.ClientConn

	handler proxycmd.HandlerServiceClient
	stats   statscmd.StatsServiceClient
}

func NewXrayDriver(options config.XrayConfig) *XrayDriver {
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
	defer d.closeConn()
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

// ClientService

func (d *XrayDriver) Upsert(ctx context.Context, u domain.User) error {
	if err := d.ensureConn(ctx); err != nil {
		return err
	}

	user, err := d.toXrayUser(u)
	if err != nil {
		return err
	}
	op := &proxycmd.AddUserOperation{User: user}
	req := &proxycmd.AlterInboundRequest{
		Tag:       d.cfg.InboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	ctx, cancel := context.WithTimeout(ctx, d.cfg.OpTimeout)
	defer cancel()
	_, err = d.handler.AlterInbound(ctx, req)
	return err
}

func (d *XrayDriver) Remove(ctx context.Context, userID string) error {
	if err := d.ensureConn(ctx); err != nil {
		return err
	}

	op := &proxycmd.RemoveUserOperation{Email: userID}
	req := &proxycmd.AlterInboundRequest{
		Tag:       d.cfg.InboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	ctx, cancel := context.WithTimeout(ctx, d.cfg.OpTimeout)
	defer cancel()
	_, err := d.handler.AlterInbound(ctx, req)
	return err
}

func (d *XrayDriver) ListUsers(ctx context.Context) ([]domain.User, error) {
	return nil, errors.New("ListUsers is not supported by Xray API")
}

// Stats

func (d *XrayDriver) Stats(ctx context.Context) (map[string]any, error) {
	// TODO when ListUsers will be realize

	var out map[string]any
	var emails []string

	out = map[string]any{}

	for _, email := range emails {
		uUp := fmt.Sprintf("user>>>%s>>>traffic>>>uplink", email)
		uDown := fmt.Sprintf("user>>>%s>>>traffic>>>downlink", email)
		uu, _ := d.getCounter(ctx, uUp)
		ud, _ := d.getCounter(ctx, uDown)
		if uu > 0 || ud > 0 {
			out[fmt.Sprintf("user.%s.uplink_bytes", email)] = uu
			out[fmt.Sprintf("user.%s.downlink_bytes", email)] = ud
		}
	}

	return out, nil
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

	// todo GRPCCreds  credentials.TransportCredentials

	/*	var opts []grpc.DialOption
		if d.cfg.GRPCCreds != nil {
			opts = append(opts, grpc.WithTransportCredentials(d.cfg.GRPCCreds))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}*/

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.NewClient(d.cfg.APIAddr, opts...)
	if err != nil {
		return fmt.Errorf("xray grpc client failed: %w", err)
	}

	d.conn = conn
	d.handler = proxycmd.NewHandlerServiceClient(conn)
	d.stats = statscmd.NewStatsServiceClient(conn)
	return nil
}

func (d *XrayDriver) toXrayUser(user domain.User) (*protocol.User, error) {
	email := user.ID // или c.Email
	level := uint32(0)
	if user.Creds["xray-level"] != "0" {
		if n, err := strconv.ParseUint(user.Creds["xray-level"], 10, 32); err == nil {
			level = uint32(n)
		}
	}

	switch d.cfg.Protocol {
	case vlessProtocol:
		if user.ID == "" {
			return nil, errors.New("missing UUID for VLESS user")
		}
		acc := &vless.Account{
			Id:   user.ID,
			Flow: d.cfg.VlessFlow,
		}
		return &protocol.User{
			Email:   email,
			Level:   level,
			Account: serial.ToTypedMessage(acc),
		}, nil

	case vmessProtocol:
		if user.ID == "" {
			return nil, errors.New("missing UUID for VMess user")
		}
		acc := &vmess.Account{
			Id:               user.ID,
			SecuritySettings: nil,
		}
		return &protocol.User{
			Email:   email,
			Level:   level,
			Account: serial.ToTypedMessage(acc),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol: %q (expected \"vless\" or \"vmess\")", d.cfg.Protocol)
	}
}

func (d *XrayDriver) getCounter(ctx context.Context, name string) (int64, error) {
	resp, err := d.stats.GetStats(ctx, &statscmd.GetStatsRequest{Name: name, Reset_: false})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			return 0, nil
		}
		return 0, err
	}
	if resp.GetStat() == nil {
		return 0, nil
	}
	return resp.GetStat().GetValue(), nil
}

func (d *XrayDriver) closeConn() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		_ = d.conn.Close()
		d.conn = nil
		d.handler = nil
		d.stats = nil
	}
}
