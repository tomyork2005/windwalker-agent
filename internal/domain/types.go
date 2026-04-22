package domain

import (
	"time"
)

type User struct {
	ID         string
	AccountID  string
	DriverType string
	ExpiresAt  time.Time
}

type Meta struct {
	RequestID string
	Seq       uint64
}

type UserStat struct {
	UserID  string
	BytesRX uint64
	BytesTX uint64
}

type StatsAll struct {
	Users        []UserStat
	TotalBytesRX uint64
	TotalBytesTX uint64
}
