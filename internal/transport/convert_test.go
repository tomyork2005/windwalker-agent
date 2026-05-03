package transport

import (
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/model"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUserFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *controlpb.User
		want *model.User
	}{
		{name: "nil -> nil", in: nil, want: nil},
		{
			name: "full",
			in:   &controlpb.User{UserId: "u-1", DriverType: "xray"},
			want: &model.User{ID: "u-1", DriverType: "xray"},
		},
		{
			name: "empty fields preserved",
			in:   &controlpb.User{},
			want: &model.User{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, userFromProto(tc.in))
		})
	}
}

func TestUpsertAckToProto(t *testing.T) {
	t.Parallel()

	gen := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	in := &model.OutboundUpsert{
		RequestID:   "req-1",
		UserID:      "u-1",
		AgentID:     "a-1",
		DriverType:  "xray",
		VlessURI:    "vless://x",
		GeneratedAt: gen,
	}
	want := &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: "req-1",
		Body: &controlpb.Response_Upsert{Upsert: &controlpb.UserUpsertResponse{
			Creds: &controlpb.UserCreds{
				UserId:      "u-1",
				AgentId:     "a-1",
				DriverType:  "xray",
				GeneratedAt: timestamppb.New(gen),
				Config: &controlpb.UserCreds_Vless{Vless: &controlpb.VlessCreds{
					Uri: "vless://x",
				}},
			},
		}},
	}}}

	got := upsertAckToProto(in)
	assert.True(t, proto.Equal(want, got), "want=%v got=%v", want, got)
	assert.Equal(t, gen.UTC(), got.GetResp().GetUpsert().GetCreds().GetGeneratedAt().AsTime())
}

func TestRemoveAckToProto(t *testing.T) {
	t.Parallel()

	got := removeAckToProto(&model.OutboundRemove{RequestID: "req-2"})
	want := &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: "req-2",
		Body:      &controlpb.Response_Remove{Remove: &controlpb.UserRemoveResponse{}},
	}}}
	assert.True(t, proto.Equal(want, got))
}

func TestErrorToProto(t *testing.T) {
	t.Parallel()

	got := errorToProto(&model.OutboundError{RequestID: "req-3", Error: "boom"})
	want := &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: "req-3",
		Body:      &controlpb.Response_Error{Error: "boom"},
	}}}
	assert.True(t, proto.Equal(want, got))
}

func TestStatsToProto(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, 5, 4, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   *model.OutboundStats
		want *controlpb.AgentToControl
	}{
		{
			name: "with users",
			in: &model.OutboundStats{
				AgentID:       "a-1",
				UptimeSeconds: 42,
				WindowEnd:     end,
				Users: []model.UserUsage{
					{UserID: "u-1", BytesUp: 100, BytesDown: 200, IPCount: 1},
					{UserID: "u-2", BytesUp: 300, BytesDown: 400, IPCount: 2},
				},
			},
			want: &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Stats{Stats: &controlpb.Stats{
				AgentId:       "a-1",
				UptimeSeconds: 42,
				WindowEnd:     timestamppb.New(end),
				Users: []*controlpb.UserUsage{
					{UserId: "u-1", BytesUp: 100, BytesDown: 200, IpCount: 1},
					{UserId: "u-2", BytesUp: 300, BytesDown: 400, IpCount: 2},
				},
			}}},
		},
		{
			name: "empty users",
			in: &model.OutboundStats{
				AgentID:       "a-1",
				UptimeSeconds: 0,
				WindowEnd:     end,
			},
			want: &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Stats{Stats: &controlpb.Stats{
				AgentId:       "a-1",
				UptimeSeconds: 0,
				WindowEnd:     timestamppb.New(end),
				Users:         []*controlpb.UserUsage{},
			}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := statsToProto(tc.in)
			assert.True(t, proto.Equal(tc.want, got), "want=%v got=%v", tc.want, got)
		})
	}
}
