package xray

import (
	"context"
	"fmt"
	"sort"

	proxycmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
)

func (d *Driver) AddUser(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("add user: user_id is empty")
	}
	email := emailFor(userID)

	existing, err := d.handler.GetInboundUsers(ctx, &proxycmd.GetInboundUserRequest{
		Tag:   d.cfg.InboundTag,
		Email: email,
	})
	if err != nil {
		return fmt.Errorf("add user: get inbound users: %w", err)
	}

	for _, u := range existing.GetUsers() {
		if u.GetEmail() == email {
			return nil
		}
	}

	xrayUser := &protocol.User{
		Email: email,
		Level: d.cfg.Level,
		Account: serial.ToTypedMessage(&vless.Account{
			Id:         userID,
			Flow:       d.cfg.Flow,
			Encryption: "",
		}),
	}
	op := &proxycmd.AddUserOperation{User: xrayUser}
	req := &proxycmd.AlterInboundRequest{
		Tag:       d.cfg.InboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	if _, err := d.handler.AlterInbound(ctx, req); err != nil {
		if errMatches(err, "already") {
			d.log.Debug("xray: user already exists, treating as success", "user_id", userID)
			return nil
		}
		return fmt.Errorf("add user: alter inbound: %w", err)
	}

	return nil
}

func (d *Driver) RemoveUser(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("remove user: user_id is empty")
	}
	email := emailFor(userID)

	op := &proxycmd.RemoveUserOperation{Email: email}
	req := &proxycmd.AlterInboundRequest{
		Tag:       d.cfg.InboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	if _, err := d.handler.AlterInbound(ctx, req); err != nil {
		if errMatches(err, "not found") {
			d.log.Debug("xray: user not found on remove, treating as success", "user_id", userID)
			return nil
		}
		return fmt.Errorf("remove user: alter inbound: %w", err)
	}

	return nil
}

func (d *Driver) ListUsers(ctx context.Context) ([]string, error) {
	resp, err := d.handler.GetInboundUsers(ctx, &proxycmd.GetInboundUserRequest{
		Tag:   d.cfg.InboundTag,
		Email: "",
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	out := make([]string, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		uid, ok := userIDFromEmail(u.GetEmail())
		if !ok {
			d.log.Warn("xray: found user with non-managed email, skipping", "email", u.GetEmail())
			continue
		}

		out = append(out, uid)
	}
	sort.Strings(out)

	return out, nil
}
