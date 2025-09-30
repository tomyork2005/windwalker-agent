package config

import (
	"github.com/google/uuid"
	"log"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	Env                 string `yaml:"env" env-default:"local"`
	SQLiteConfig        `yaml:"storage_sqlite"`
	XrayConfig          `yaml:"driver_xray"`
	TransportGrpcConfig `yaml:"transport_grpc"`
}

type SQLiteConfig struct {
	Path            string        `yaml:"path" env-default:"./data/agent.db"`
	BusyTimeout     time.Duration `yaml:"busy_timeout" env-default:"5s"`
	Wal             bool          `yaml:"wal" env-default:"true"`
	SynchronousFull bool          `yaml:"synchronous_full" env-default:"false"`
	ForeignKeys     bool          `yaml:"foreign_keys" env-default:"false"`
}

type XrayConfig struct {
	ServiceName string        `yaml:"service_name" env-default:"xray"`
	APIAddr     string        `yaml:"api_addr" env-default:"127.0.0.1:10085"`
	InboundTag  string        `yaml:"inbound_tag" env-default:"vless-in"`
	Protocol    string        `yaml:"protocol" env-default:"vless"`
	OpTimeout   time.Duration `yaml:"op_timeout" env-default:"5s"`
	VlessFlow   string        `yaml:"vless_flow" env-default:"xtls-rprx-vision"`
}

type TransportGrpcConfig struct {
	Address     string   `yaml:"address" env-default:""`
	AgentID     string   `yaml:"agent_id" env-default:""`
	InstanceID  string   `yaml:"instance_id" env-default:""`
	Region      string   `yaml:"region" env-default:""`
	Version     string   `yaml:"version" env-default:""`
	DriverTypes []string `yaml:"driver_types" env-default:"xray"`

	HeartbeatPeriod time.Duration `yaml:"heartbeat_period" env-default:"5s"`
	SendQueueSize   int           `yaml:"send_queue_size" env-default:"0"`
	ReconnectMin    time.Duration `yaml:"reconnect_min" env-default:"5s"`
	ReconnectMax    time.Duration `yaml:"reconnect_max" env-default:"5s"`
	DialTimeout     time.Duration `yaml:"dial_timeout" env-default:"5s"`
}

func MustLoadConfig() *Config {
	configEnv := os.Getenv("CONFIG_PATH")
	if configEnv == "" {
		log.Fatal("configs path can`t be empty")
	}

	if _, err := os.Stat(configEnv); os.IsNotExist(err) {
		log.Fatalf("configs file doesn't exist %s", err)
	}

	var config Config
	if err := cleanenv.ReadConfig(configEnv, &config); err != nil {
		log.Fatalf("fail with read configs %s", err)
	}

	config.InstanceID = uuid.NewString()

	return &config
}
