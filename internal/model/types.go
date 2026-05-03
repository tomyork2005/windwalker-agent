package model

import "time"

type User struct {
	ID         string
	DriverType string
}

type UserUsage struct {
	UserID    string
	BytesUp   uint64
	BytesDown uint64
	IPCount   uint32
}

type Task struct {
	RequestID  string
	UserID     string
	DriverType string
	Kind       TaskKind // "upsert" / "remove"
	ReceivedAt time.Time
}

type TaskKind string

const (
	TaskUpsert TaskKind = "upsert"
	TaskRemove TaskKind = "remove"
)
