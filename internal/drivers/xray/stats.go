package xray

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agent/internal/model"

	statscmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (d *Driver) CollectStats(ctx context.Context) ([]*model.UserUsage, error) {
	resp, err := d.stats.QueryStats(ctx, &statscmd.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  false,
	})
	if err != nil {
		return nil, fmt.Errorf("collect stats: query stats: %w", err)
	}

	byUserID := make(map[string]*model.UserUsage, len(resp.GetStat()))
	for _, st := range resp.GetStat() {
		email, dir, ok := parseStatName(st.GetName())
		if !ok {
			continue
		}

		id, ok := userIDFromEmail(email)
		if !ok {
			d.log.Warn("xray: skip stat for non-managed email", "email", email)
			continue
		}

		u, ok := byUserID[id]
		if !ok {
			u = &model.UserUsage{UserID: id}
			byUserID[id] = u
		}

		switch dir {
		case "uplink":
			u.BytesUp = uint64(st.GetValue())
		case "downlink":
			u.BytesDown = uint64(st.GetValue())
		}
	}

	for id, usage := range byUserID {
		ips, err := d.fetchOnlineIPCount(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("collect stats: ip list for %s: %w", id, err)
		}

		usage.IPCount = ips
	}

	out := make([]*model.UserUsage, 0, len(byUserID))
	for _, user := range byUserID {
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })

	return out, nil
}

func (d *Driver) fetchOnlineIPCount(ctx context.Context, userID string) (uint32, error) {
	name := "user>>>" + emailFor(userID) + ">>>online"

	resp, err := d.stats.GetStatsOnlineIpList(ctx, &statscmd.GetStatsRequest{
		Name:   name,
		Reset_: false,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return 0, nil
		}
		if errMatches(err, "not found") {
			return 0, nil
		}
		return 0, err
	}
	return uint32(len(resp.GetIps())), nil
}

// parseStatName splits an Xray per-user counter name like
// "user>>>{email}>>>traffic>>>{uplink|downlink}" into its parts.
// Returns ok=false for any other shape.
func parseStatName(name string) (email, direction string, ok bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 {
		return "", "", false
	}

	if parts[0] != "user" || parts[2] != "traffic" {
		return "", "", false
	}

	dir := parts[3]
	if dir != "uplink" && dir != "downlink" {
		return "", "", false
	}

	return parts[1], dir, true
}
