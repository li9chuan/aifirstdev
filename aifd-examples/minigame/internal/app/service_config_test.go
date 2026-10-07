package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

func TestLoadServiceFileConfig(t *testing.T) {
	path := writeServiceConfig(t, `
[log]
level = "debug"
stdout = true
dir = "./logs"

[rpc.nats]
servers = ["nats://127.0.0.1:4222"]
timeout = "3s"

[proxy_broker]
enabled = true
config_file = "./configs/nats/proxy-local.conf"
ready_timeout = "10s"
shutdown_timeout = "10s"

[rdb.redis]
addrs = ["127.0.0.1:6379"]
password = ""
dial_timeout = "1s"
read_timeout = "1s"
write_timeout = "1s"

[proxy]
max_message_bytes = 4194304
duplicate_policy = "kick_old"
`)
	cfg, err := LoadServiceFileConfig(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadServiceFileConfig error = %v", err)
	}
	logCfg, err := cfg.LoggingConfig("minigame")
	if err != nil {
		t.Fatalf("LoggingConfig error = %v", err)
	}
	if !logCfg.AlsoStdout || logCfg.SourceService != "minigame" {
		t.Fatalf("log config = %+v", logCfg)
	}
	natsCfg, err := cfg.NATSConfig("minigame-proxy", "")
	if err != nil {
		t.Fatalf("NATSConfig error = %v", err)
	}
	if natsCfg.Options.Name != "minigame-proxy" || natsCfg.Options.Servers[0] != "nats://127.0.0.1:4222" {
		t.Fatalf("nats config = %+v", natsCfg.Options)
	}
	if natsCfg.Options.Timeout != 3*time.Second {
		t.Fatalf("nats timeout = %v", natsCfg.Options.Timeout)
	}
	if !cfg.ProxyBroker.Enabled || cfg.ProxyBroker.ConfigFile != "./configs/nats/proxy-local.conf" {
		t.Fatalf("proxy broker config = %+v", cfg.ProxyBroker)
	}
	if cfg.ProxyBroker.ReadyTimeout != 10*time.Second || cfg.ProxyBroker.ShutdownTimeout != 10*time.Second {
		t.Fatalf("proxy broker timeouts = %+v", cfg.ProxyBroker)
	}
	proxyCfg, err := cfg.Proxy.Apply(proxyws.Config{})
	if err != nil {
		t.Fatalf("Proxy.Apply error = %v", err)
	}
	if proxyCfg.MaxMessageBytes != 4194304 || proxyCfg.DuplicatePolicy != proxyws.DuplicateKickOld {
		t.Fatalf("proxy config = %+v", proxyCfg)
	}
	if _, err := cfg.RDB.Redis.RedisOptions(); err != nil {
		t.Fatalf("RedisOptions error = %v", err)
	}
}

func TestLoadServiceFileConfigRejectsRedisDBIndex(t *testing.T) {
	path := writeServiceConfig(t, `
[rdb.redis]
addrs = ["127.0.0.1:6379"]
db = 0
`)
	if _, err := LoadServiceFileConfig(context.Background(), path); err == nil {
		t.Fatal("LoadServiceFileConfig error = nil, want unknown db field error")
	}
}

func TestNATSConfigCLIOverride(t *testing.T) {
	cfg := ServiceFileConfig{}
	natsCfg, err := cfg.NATSConfig("svc", "nats://127.0.0.1:5222")
	if err != nil {
		t.Fatalf("NATSConfig error = %v", err)
	}
	if strings.Join(natsCfg.Options.Servers, ",") != "nats://127.0.0.1:5222" {
		t.Fatalf("servers = %#v", natsCfg.Options.Servers)
	}
}

func TestProxyBrokerExampleConfigsParse(t *testing.T) {
	tests := []struct {
		name       string
		wantRoutes int
	}{
		{name: "proxy-local.conf", wantRoutes: 0},
		{name: "proxy-a.conf", wantRoutes: 2},
		{name: "proxy-b.conf", wantRoutes: 2},
		{name: "proxy-c.conf", wantRoutes: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := natsserver.ProcessConfigFile(filepath.Join("..", "..", "configs", "nats", tt.name))
			if err != nil {
				t.Fatalf("ProcessConfigFile error = %v", err)
			}
			if opts.ServerName == "" {
				t.Fatal("server_name is empty")
			}
			if tt.wantRoutes == 0 {
				if len(opts.Routes) != 0 {
					t.Fatalf("routes = %d, want 0", len(opts.Routes))
				}
				return
			}
			if opts.Cluster.Name != "minigame-proxy" || opts.Cluster.Port == 0 {
				t.Fatalf("cluster = %+v, want minigame-proxy with listen port", opts.Cluster)
			}
			if len(opts.Routes) != tt.wantRoutes {
				t.Fatalf("routes = %d, want %d", len(opts.Routes), tt.wantRoutes)
			}
		})
	}
}

func writeServiceConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.toml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
