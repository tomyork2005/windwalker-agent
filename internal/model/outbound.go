package model

import "time"

type OutboundUpsert struct {
	RequestID   string
	UserID      string
	AgentID     string
	DriverType  string
	VlessURI    string
	GeneratedAt time.Time
}

type OutboundRemove struct {
	RequestID string
}

type OutboundError struct {
	RequestID string
	Error     string
}

type OutboundStats struct {
	AgentID       string
	UptimeSeconds uint32
	WindowEnd     time.Time
	Users         []UserUsage
}
