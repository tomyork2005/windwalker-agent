package transport

import (
	controlpb "agent/api/control"
	"agent/internal/model"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func userFromProto(u *controlpb.User) *model.User {
	if u == nil {
		return nil
	}
	return &model.User{
		ID:         u.GetUserId(),
		DriverType: u.GetDriverType(),
	}
}

func upsertAckToProto(m *model.OutboundUpsert) *controlpb.AgentToControl {
	return &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: m.RequestID,
		Body: &controlpb.Response_Upsert{Upsert: &controlpb.UserUpsertResponse{
			Creds: &controlpb.UserCreds{
				UserId:      m.UserID,
				AgentId:     m.AgentID,
				DriverType:  m.DriverType,
				GeneratedAt: timestamppb.New(m.GeneratedAt),
				Config: &controlpb.UserCreds_Vless{Vless: &controlpb.VlessCreds{
					Uri: m.VlessURI,
				}},
			},
		}},
	}}}
}

func removeAckToProto(m *model.OutboundRemove) *controlpb.AgentToControl {
	return &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: m.RequestID,
		Body:      &controlpb.Response_Remove{Remove: &controlpb.UserRemoveResponse{}},
	}}}
}

func errorToProto(m *model.OutboundError) *controlpb.AgentToControl {
	return &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Resp{Resp: &controlpb.Response{
		RequestId: m.RequestID,
		Body:      &controlpb.Response_Error{Error: m.Error},
	}}}
}

func statsToProto(m *model.OutboundStats) *controlpb.AgentToControl {
	users := make([]*controlpb.UserUsage, 0, len(m.Users))
	for _, u := range m.Users {
		users = append(users, &controlpb.UserUsage{
			UserId:    u.UserID,
			BytesUp:   u.BytesUp,
			BytesDown: u.BytesDown,
			IpCount:   u.IPCount,
		})
	}
	return &controlpb.AgentToControl{Msg: &controlpb.AgentToControl_Stats{Stats: &controlpb.Stats{
		AgentId:       m.AgentID,
		UptimeSeconds: m.UptimeSeconds,
		WindowEnd:     timestamppb.New(m.WindowEnd),
		Users:         users,
	}}}
}
