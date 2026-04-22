package driver

import (
	controlpb "agent/api/control"
	"context"
	"errors"

	"agent/internal/domain"
)

type Multiplexer struct {
	drivers map[string]Driver
}

type Driver interface {
	Name() string
	Upsert(ctx context.Context, user domain.User) error
	Remove(ctx context.Context, userID string) error
	BuildCreds(user domain.User) (*controlpb.UserCreds, error)
}

func NewMultiplexer(drivers ...Driver) *Multiplexer {
	driversMap := make(map[string]Driver)
	for _, driver := range drivers {
		driversMap[driver.Name()] = driver
	}
	return &Multiplexer{drivers: driversMap}
}

func (m *Multiplexer) Upsert(ctx context.Context, user domain.User) error {
	d, ok := m.drivers[user.DriverType]
	if !ok {
		return errors.New("driver not found")
	}
	return d.Upsert(ctx, user)
}

func (m *Multiplexer) Remove(ctx context.Context, userID string, driverType string) error {
	d, ok := m.drivers[driverType]
	if !ok {
		return errors.New("driver not found")
	}
	return d.Remove(ctx, userID)
}

func (m *Multiplexer) BuildCreds(user domain.User) (*controlpb.UserCreds, error) {
	d, ok := m.drivers[user.DriverType]
	if !ok {
		return nil, errors.New("driver not found")
	}
	return d.BuildCreds(user)
}
