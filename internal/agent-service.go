package internal

import "context"

type TaskStorage interface {
	Add(id string) error
	Has(id string) (bool, error)
}

type Client struct {
	ID        string
	PublicKey string
	Email     string
	Limits    *Limits
}

type NetworkDriver interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Health(ctx context.Context) error
}

type StatsProvider interface {
	Stats(ctx context.Context) (map[string]any, error)
}

type ClientManager interface {
	AddClient(ctx context.Context, client Client) error
	DeleteClient(ctx context.Context, id string) error
	ListUsers(ctx context.Context) ([]Client, error)
}

type VPNDriver interface {
	NetworkDriver
	ClientManager
	StatsProvider
}

type AgentService struct {
	drivers map[string]VPNDriver
}
