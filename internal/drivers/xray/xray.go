package xray

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"agent/internal/config"

	proxycmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const emailDomain = "@xray.com"

type Driver struct {
	cfg  config.XrayConfig
	log  *slog.Logger
	conn *grpc.ClientConn

	handler proxycmd.HandlerServiceClient
	stats   statscmd.StatsServiceClient
}

func New(ctx context.Context, cfg config.XrayConfig, log *slog.Logger) (*Driver, error) {
	if log == nil {
		log = slog.Default()
	}

	conn, err := grpc.NewClient(cfg.APIAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("xray grpc client: %w", err)
	}

	return &Driver{
		cfg:     cfg,
		log:     log,
		conn:    conn,
		handler: proxycmd.NewHandlerServiceClient(conn),
		stats:   statscmd.NewStatsServiceClient(conn),
	}, nil
}

func (d *Driver) Close() error {
	return d.conn.Close()
}

func emailFor(userID string) string {
	return userID + emailDomain
}

func userIDFromEmail(email string) (string, bool) {
	if !strings.HasSuffix(email, emailDomain) {
		return "", false
	}
	return strings.TrimSuffix(email, emailDomain), true
}

func errMatches(err error, needle string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), needle)
}
