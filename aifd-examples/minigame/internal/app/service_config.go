package app

import (
	"context"
	"strings"
	"time"

	configviper "github.com/li9chuan/aifirstdev/aifd-config/adapters/viper"
	"github.com/li9chuan/aifirstdev/aifd-observe/logging"
	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
)

// ServiceFileConfig collects shared framework config for minigame service entrypoints.
type ServiceFileConfig struct {
	Log         logging.FileConfig    `mapstructure:"log"`
	RPC         RPCFileConfig         `mapstructure:"rpc"`
	RDB         RDBFileConfig         `mapstructure:"rdb"`
	Proxy       proxyws.FileConfig    `mapstructure:"proxy"`
	ProxyBroker ProxyBrokerFileConfig `mapstructure:"proxy_broker"`
}

// RPCFileConfig groups RPC adapter config.
type RPCFileConfig struct {
	NATS rpcnats.FileConfig `mapstructure:"nats"`
}

// RDBFileConfig groups Redis connection and DB behavior config.
type RDBFileConfig struct {
	Redis rdb.RedisFileConfig `mapstructure:"redis"`
	DB    rdb.DBFileConfig    `mapstructure:"db"`
}

// ProxyBrokerFileConfig keeps proxy-broker file config in the example layer without importing broker runtime code.
type ProxyBrokerFileConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	ConfigFile      string        `mapstructure:"config_file"`
	ReadyTimeout    time.Duration `mapstructure:"ready_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// LoadServiceFileConfig loads shared service config from --conf and environment.
func LoadServiceFileConfig(ctx context.Context, path string) (ServiceFileConfig, error) {
	return configviper.Load[ServiceFileConfig](ctx, configviper.Options{
		ConfigFile:       path,
		AllowMissingFile: path == "",
		EnvPrefix:        "AIFD",
		EnvKeys:          serviceFileConfigEnvKeys(),
	})
}

func serviceFileConfigEnvKeys() []string {
	keys := make([]string, 0, 32)
	keys = append(keys, logging.FileConfigEnvKeys("log")...)
	keys = append(keys, rpcnats.FileConfigEnvKeys("rpc.nats")...)
	keys = append(keys, rdb.RedisFileConfigEnvKeys("rdb.redis")...)
	keys = append(keys, rdb.DBFileConfigEnvKeys("rdb.db")...)
	keys = append(keys, proxyws.FileConfigEnvKeys("proxy")...)
	keys = append(keys, proxyBrokerFileConfigEnvKeys("proxy_broker")...)
	return keys
}

// LoggingConfig returns logging config with the service name owned by the entrypoint.
func (cfg ServiceFileConfig) LoggingConfig(serviceName string) (logging.Config, error) {
	logCfg := cfg.Log
	if logCfg.SourceService == "" {
		logCfg.SourceService = serviceName
	}
	return logCfg.ToConfig()
}

// NATSConfig returns NATS RPC config, with CLI URL taking precedence over shared config.
func (cfg ServiceFileConfig) NATSConfig(serviceName string, overrideURL string) (rpcnats.Config, error) {
	natsCfg := cfg.RPC.NATS
	if overrideURL != "" {
		natsCfg.Servers = []string{overrideURL}
	}
	if len(natsCfg.Servers) == 0 {
		natsCfg.Servers = []string{defaultNATSURL}
	}
	return natsCfg.ToConfig(serviceName)
}

func proxyBrokerFileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix, "enabled", "config_file", "ready_timeout", "shutdown_timeout")
}

func prefixedKeys(prefix string, keys ...string) []string {
	prefix = strings.Trim(prefix, ".")
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if prefix == "" {
			out = append(out, key)
			continue
		}
		out = append(out, prefix+"."+key)
	}
	return out
}
