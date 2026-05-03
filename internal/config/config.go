package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	Env        string              `yaml:"env"        env:"AGENT_ENV"   env-default:"prod"`
	AgentID    string              `yaml:"agent_id"   env:"AGENT_ID"`
	Storage    SQLiteConfig        `yaml:"storage"`
	DriverXray XrayConfig          `yaml:"driver_xray"`
	Transport  TransportGrpcConfig `yaml:"transport"`
	Worker     WorkerConfig        `yaml:"worker"`
}

type SQLiteConfig struct {
	Path            string        `yaml:"path"             env-default:"/var/lib/skywalker-agent/agent.db"`
	BusyTimeout     time.Duration `yaml:"busy_timeout"     env-default:"5s"`
	Wal             bool          `yaml:"wal"              env-default:"true"`
	SynchronousFull bool          `yaml:"synchronous_full" env-default:"false"`
	ForeignKeys     bool          `yaml:"foreign_keys"     env-default:"true"`
}

type XrayConfig struct {
	APIAddr    string        `yaml:"api_addr"    env-default:"127.0.0.1:10085"`
	InboundTag string        `yaml:"inbound_tag" env-default:"vless-in"`
	OpTimeout  time.Duration `yaml:"op_timeout"  env-default:"5s"`
	Flow       string        `yaml:"flow"        env-default:"xtls-rprx-vision"`
	Level      uint32        `yaml:"level"       env-default:"0"`

	PublicHost  string `yaml:"public_host"  env:"AGENT_PUBLIC_HOST"`
	Port        uint32 `yaml:"port"         env-default:"443"`
	SNI         string `yaml:"sni"`
	PublicKey   string `yaml:"public_key"`
	ShortID     string `yaml:"short_id"`
	Fingerprint string `yaml:"fingerprint"  env-default:"chrome"`
}

type TransportGrpcConfig struct {
	Address       string        `yaml:"address"`
	AgentID       string        `yaml:"-"`
	InstanceID    string        `yaml:"-"`
	Region        string        `yaml:"region"`
	Version       string        `yaml:"version"`
	SendQueueSize int           `yaml:"send_queue_size" env-default:"128"`
	ReconnectMin  time.Duration `yaml:"reconnect_min"   env-default:"1s"`
	ReconnectMax  time.Duration `yaml:"reconnect_max"   env-default:"60s"`
	DialTimeout   time.Duration `yaml:"dial_timeout"    env-default:"5s"`
}

// Field order, names and types must mirror service.WorkerConfig so the struct
// is convertible (service.WorkerConfig(cfg.Worker)) — keeps service decoupled
// from this package while still letting cmd wire it from YAML.
type WorkerConfig struct {
	TaskPollInterval time.Duration `yaml:"task_poll_interval" env-default:"1s"`
	StatsInterval    time.Duration `yaml:"stats_interval"     env-default:"30s"`
	SyncInterval     time.Duration `yaml:"sync_interval"      env-default:"1m"`
	TaskBatchLimit   int           `yaml:"task_batch_limit"   env-default:"32"`
	AgentID          string        `yaml:"-"`
}

func MustLoadConfig() *Config {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		fatal("CONFIG_PATH env is empty")
	}
	if _, err := os.Stat(path); err != nil {
		fatal("CONFIG_PATH is unreadable", "path", path, "err", err)
	}

	var cfg Config
	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		fatal("read config failed", "path", path, "err", err)
	}

	if err := validate(&cfg); err != nil {
		fatal("config validation failed", "err", err)
	}

	cfg.Transport.AgentID = cfg.AgentID
	cfg.Worker.AgentID = cfg.AgentID
	cfg.Transport.InstanceID = uuid.NewString()

	return &cfg
}

func validate(c *Config) error {
	required := []struct {
		field string
		value string
	}{
		{"agent_id", c.AgentID},
		{"transport.address", c.Transport.Address},
		{"driver_xray.public_host", c.DriverXray.PublicHost},
		{"driver_xray.sni", c.DriverXray.SNI},
		{"driver_xray.public_key", c.DriverXray.PublicKey},
		{"driver_xray.short_id", c.DriverXray.ShortID},
		{"storage.path", c.Storage.Path},
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("%s is required", r.field)
		}
	}
	return nil
}

func fatal(msg string, args ...any) {
	slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error(msg, args...)
	os.Exit(1)
}
