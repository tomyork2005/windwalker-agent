package transport

import (
	"context"

	controlpb "agent/api/control"
	"agent/internal/model"
)

func (c *Client) SendUpsertAck(ctx context.Context, m *model.OutboundUpsert) error {
	return c.enqueue(ctx, upsertAckToProto(m))
}

func (c *Client) SendRemoveAck(ctx context.Context, m *model.OutboundRemove) error {
	return c.enqueue(ctx, removeAckToProto(m))
}

func (c *Client) SendError(ctx context.Context, m *model.OutboundError) error {
	return c.enqueue(ctx, errorToProto(m))
}

func (c *Client) SendStats(ctx context.Context, m *model.OutboundStats) error {
	return c.enqueue(ctx, statsToProto(m))
}

func (c *Client) enqueue(ctx context.Context, m *controlpb.AgentToControl) error {
	select {
	case c.sendQ <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
