package viper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testConfig struct {
	Log   testLogConfig   `mapstructure:"log"`
	RPC   testRPCConfig   `mapstructure:"rpc"`
	RDB   testRDBConfig   `mapstructure:"rdb"`
	Proxy testProxyConfig `mapstructure:"proxy"`
}

type testLogConfig struct {
	Level  string `mapstructure:"level"`
	Stdout bool   `mapstructure:"stdout"`
	Dir    string `mapstructure:"dir"`
}

type testRPCConfig struct {
	NATS testNATSConfig `mapstructure:"nats"`
}

type testNATSConfig struct {
	Servers []string      `mapstructure:"servers"`
	Timeout time.Duration `mapstructure:"timeout"`
}

type testRDBConfig struct {
	Redis testRedisConfig `mapstructure:"redis"`
}

type testRedisConfig struct {
	Addrs        []string      `mapstructure:"addrs"`
	Password     string        `mapstructure:"password"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type testProxyConfig struct {
	MaxMessageBytes int64  `mapstructure:"max_message_bytes"`
	DuplicatePolicy string `mapstructure:"duplicate_policy"`
}

func TestLoadIntoDecodesTOML(t *testing.T) {
	path := writeConfigFile(t, "aifd.toml", `
[log]
level = "info"
stdout = true
dir = "./logs"

[rpc.nats]
servers = ["nats://127.0.0.1:4222"]
timeout = "3s"

[rdb.redis]
addrs = ["127.0.0.1:6379"]
password = ""
dial_timeout = "1s"
read_timeout = "2s"
write_timeout = "3s"

[proxy]
max_message_bytes = 4194304
duplicate_policy = "kick_old"
`)
	var cfg testConfig
	if err := LoadInto(context.Background(), Options{ConfigFile: path}, &cfg); err != nil {
		t.Fatalf("LoadInto error = %v", err)
	}
	if cfg.Log.Level != "info" || !cfg.Log.Stdout || cfg.Log.Dir != "./logs" {
		t.Fatalf("log config = %+v", cfg.Log)
	}
	if len(cfg.RPC.NATS.Servers) != 1 || cfg.RPC.NATS.Servers[0] != "nats://127.0.0.1:4222" {
		t.Fatalf("servers = %#v", cfg.RPC.NATS.Servers)
	}
	if cfg.RPC.NATS.Timeout != 3*time.Second {
		t.Fatalf("timeout = %v", cfg.RPC.NATS.Timeout)
	}
	if cfg.RDB.Redis.DialTimeout != time.Second || cfg.RDB.Redis.ReadTimeout != 2*time.Second || cfg.RDB.Redis.WriteTimeout != 3*time.Second {
		t.Fatalf("redis config = %+v", cfg.RDB.Redis)
	}
	if cfg.Proxy.MaxMessageBytes != 4194304 || cfg.Proxy.DuplicatePolicy != "kick_old" {
		t.Fatalf("proxy config = %+v", cfg.Proxy)
	}
}

func TestLoadDecodesYAMLAndJSON(t *testing.T) {
	yamlPath := writeConfigFile(t, "aifd.yaml", `
log:
  level: warn
rpc:
  nats:
    servers:
      - nats://127.0.0.1:4222
    timeout: 500ms
`)
	yamlCfg, err := Load[testConfig](context.Background(), Options{ConfigFile: yamlPath})
	if err != nil {
		t.Fatalf("Load yaml error = %v", err)
	}
	if yamlCfg.Log.Level != "warn" || yamlCfg.RPC.NATS.Timeout != 500*time.Millisecond {
		t.Fatalf("yaml config = %+v", yamlCfg)
	}

	jsonPath := writeConfigFile(t, "aifd.json", `{
  "log": {"level": "debug"},
  "rpc": {"nats": {"servers": ["nats://127.0.0.1:4223"], "timeout": "2s"}}
}`)
	jsonCfg, err := Load[testConfig](context.Background(), Options{ConfigFile: jsonPath})
	if err != nil {
		t.Fatalf("Load json error = %v", err)
	}
	if jsonCfg.Log.Level != "debug" || jsonCfg.RPC.NATS.Timeout != 2*time.Second {
		t.Fatalf("json config = %+v", jsonCfg)
	}
}

func TestLoadIntoOverridesNestedKeysFromEnv(t *testing.T) {
	path := writeConfigFile(t, "aifd.toml", `
[rpc.nats]
servers = ["nats://127.0.0.1:4222"]
timeout = "1s"
`)
	t.Setenv("AIFD_RPC_NATS_SERVERS", "nats://127.0.0.1:4223,nats://127.0.0.1:4224")
	t.Setenv("AIFD_RPC_NATS_TIMEOUT", "2s")
	var cfg testConfig
	err := LoadInto(context.Background(), Options{
		ConfigFile: path,
		EnvPrefix:  "AIFD",
		EnvKeys: []string{
			"rpc.nats.servers",
			"rpc.nats.timeout",
		},
	}, &cfg)
	if err != nil {
		t.Fatalf("LoadInto error = %v", err)
	}
	if strings.Join(cfg.RPC.NATS.Servers, ",") != "nats://127.0.0.1:4223,nats://127.0.0.1:4224" {
		t.Fatalf("servers = %#v", cfg.RPC.NATS.Servers)
	}
	if cfg.RPC.NATS.Timeout != 2*time.Second {
		t.Fatalf("timeout = %v", cfg.RPC.NATS.Timeout)
	}
}

func TestLoadIntoFailsOnUnknownKeysByDefault(t *testing.T) {
	path := writeConfigFile(t, "aifd.toml", `
[log]
level = "info"
unused = "bad"
`)
	var cfg testConfig
	err := LoadInto(context.Background(), Options{ConfigFile: path}, &cfg)
	if err == nil {
		t.Fatal("LoadInto error = nil, want unused key error")
	}
}

func TestLoadIntoAllowsUnusedKeysWhenRequested(t *testing.T) {
	path := writeConfigFile(t, "aifd.toml", `
[log]
level = "info"
unused = "compatible"
`)
	var cfg testConfig
	if err := LoadInto(context.Background(), Options{ConfigFile: path, AllowUnusedKeys: true}, &cfg); err != nil {
		t.Fatalf("LoadInto error = %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("level = %q", cfg.Log.Level)
	}
}

func TestLoadIntoUsesValidator(t *testing.T) {
	path := writeConfigFile(t, "aifd.toml", `
[rpc.nats]
timeout = "1s"
`)
	errRequired := errors.New("rpc.nats.servers is required")
	var cfg testConfig
	err := LoadInto(context.Background(), Options{
		ConfigFile: path,
		Validator: func(out any) error {
			cfg := out.(*testConfig)
			if len(cfg.RPC.NATS.Servers) == 0 {
				return errRequired
			}
			return nil
		},
	}, &cfg)
	if !errors.Is(err, errRequired) {
		t.Fatalf("LoadInto error = %v, want %v", err, errRequired)
	}
}

func TestLoadIntoCanUseEnvOnlyConfig(t *testing.T) {
	t.Setenv("AIFD_RPC_NATS_TIMEOUT", "4s")
	var cfg testConfig
	err := LoadInto(context.Background(), Options{
		AllowMissingFile: true,
		EnvPrefix:        "AIFD",
		EnvKeys:          []string{"rpc.nats.timeout"},
	}, &cfg)
	if err != nil {
		t.Fatalf("LoadInto error = %v", err)
	}
	if cfg.RPC.NATS.Timeout != 4*time.Second {
		t.Fatalf("timeout = %v", cfg.RPC.NATS.Timeout)
	}
}

func TestLoadIntoRejectsInvalidOutput(t *testing.T) {
	if err := LoadInto(context.Background(), Options{AllowMissingFile: true}, testConfig{}); err == nil {
		t.Fatal("LoadInto error = nil, want invalid output error")
	}
}

func writeConfigFile(t *testing.T, name string, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
