package transport

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func newSenderClient(t *testing.T, queue int) *Client {
	t.Helper()
	return &Client{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sendQ: make(chan *controlpb.AgentToControl, queue),
	}
}

func drainOne(t *testing.T, c *Client) *controlpb.AgentToControl {
	t.Helper()
	select {
	case m := <-c.sendQ:
		return m
	case <-time.After(time.Second):
		t.Fatalf("nothing in sendQ")
		return nil
	}
}

func TestSenderEnqueue(t *testing.T) {
	t.Parallel()

	gen := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 4, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		send func(ctx context.Context, c *Client) error
		want *controlpb.AgentToControl
	}{
		{
			name: "SendUpsertAck",
			send: func(ctx context.Context, c *Client) error {
				return c.SendUpsertAck(ctx, &model.OutboundUpsert{
					RequestID:   "r1",
					UserID:      "u1",
					AgentID:     "a1",
					DriverType:  "xray",
					VlessURI:    "vless://x",
					GeneratedAt: gen,
				})
			},
			want: upsertAckToProto(&model.OutboundUpsert{
				RequestID: "r1", UserID: "u1", AgentID: "a1",
				DriverType: "xray", VlessURI: "vless://x", GeneratedAt: gen,
			}),
		},
		{
			name: "SendRemoveAck",
			send: func(ctx context.Context, c *Client) error {
				return c.SendRemoveAck(ctx, &model.OutboundRemove{RequestID: "r2"})
			},
			want: removeAckToProto(&model.OutboundRemove{RequestID: "r2"}),
		},
		{
			name: "SendError",
			send: func(ctx context.Context, c *Client) error {
				return c.SendError(ctx, &model.OutboundError{RequestID: "r3", Error: "boom"})
			},
			want: errorToProto(&model.OutboundError{RequestID: "r3", Error: "boom"}),
		},
		{
			name: "SendStats",
			send: func(ctx context.Context, c *Client) error {
				return c.SendStats(ctx, &model.OutboundStats{
					AgentID:       "a1",
					UptimeSeconds: 7,
					WindowEnd:     end,
					Users: []model.UserUsage{
						{UserID: "u1", BytesUp: 1, BytesDown: 2, IPCount: 3},
					},
				})
			},
			want: statsToProto(&model.OutboundStats{
				AgentID:       "a1",
				UptimeSeconds: 7,
				WindowEnd:     end,
				Users: []model.UserUsage{
					{UserID: "u1", BytesUp: 1, BytesDown: 2, IPCount: 3},
				},
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newSenderClient(t, 1)
			require.NoError(t, tc.send(context.Background(), c))
			got := drainOne(t, c)
			assert.True(t, proto.Equal(tc.want, got), "want=%v got=%v", tc.want, got)
		})
	}
}

func TestSenderCtxCancelled(t *testing.T) {
	t.Parallel()

	c := newSenderClient(t, 0) // нулевой буфер — Send будет блокироваться

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.SendStats(ctx, &model.OutboundStats{AgentID: "a1"})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSenderFullQueueBlocks(t *testing.T) {
	t.Parallel()

	c := newSenderClient(t, 1)

	// Заполняем единственный слот.
	require.NoError(t, c.SendError(context.Background(), &model.OutboundError{RequestID: "r1"}))

	// Следующий Send должен блокироваться, пока кто-то не сольёт очередь.
	done := make(chan error, 1)
	go func() {
		done <- c.SendError(context.Background(), &model.OutboundError{RequestID: "r2"})
	}()

	select {
	case <-done:
		t.Fatalf("Send unexpectedly returned while queue was full")
	case <-time.After(50 * time.Millisecond):
	}

	// Дренируем — освобождаем слот.
	<-c.sendQ

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatalf("Send did not unblock after drain")
	}
}
