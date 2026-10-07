package broker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

func TestStartOptionsInProcessConnectionAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := Start(ctx, Config{
		Options: &server.Options{
			ServerName: "broker-test",
			Host:       "127.0.0.1",
			Port:       -1,
		},
		ReadyTimeout:    time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if b.Server() == nil {
		t.Fatal("Server = nil")
	}
	if b.ClientURL() == "" {
		t.Fatal("ClientURL is empty")
	}
	nc, err := natsgo.Connect(
		b.ClientURL(),
		natsgo.InProcessServer(b.InProcessServer()),
		natsgo.Name("broker-test-client"),
	)
	if err != nil {
		t.Fatalf("Connect error = %v", err)
	}
	defer nc.Close()
	sub, err := nc.SubscribeSync("broker.test")
	if err != nil {
		t.Fatalf("SubscribeSync error = %v", err)
	}
	if err := nc.Publish("broker.test", []byte("ok")); err != nil {
		t.Fatalf("Publish error = %v", err)
	}
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatalf("NextMsg error = %v", err)
	}
	if string(msg.Data) != "ok" {
		t.Fatalf("message data = %q", msg.Data)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
}

func TestStartMissingConfigFileReturnsError(t *testing.T) {
	_, err := Start(context.Background(), Config{
		ConfigFile: filepath.Join(t.TempDir(), "missing.conf"),
	})
	if err == nil {
		t.Fatal("Start error = nil, want missing config error")
	}
}

func TestStartRequiresConfigFileOrOptions(t *testing.T) {
	_, err := Start(context.Background(), Config{})
	if err == nil {
		t.Fatal("Start error = nil, want missing config_file/options error")
	}
}

func TestStartReadyTimeoutCleansUp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	_, err = Start(context.Background(), Config{
		Options: &server.Options{
			ServerName: "broker-ready-timeout-test",
			Host:       "127.0.0.1",
			Port:       port,
		},
		ReadyTimeout:    50 * time.Millisecond,
		ShutdownTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("Start error = nil, want ready timeout")
	}
}

func TestStartConfigFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "nats.conf")
	config := []byte("server_name: broker-config-test\nhost: 127.0.0.1\nport: -1\n")
	if err := os.WriteFile(configFile, config, 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	b, err := Start(context.Background(), Config{
		ConfigFile:      configFile,
		ReadyTimeout:    time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer b.Close(context.Background())
	if b.Server().Name() != "broker-config-test" {
		t.Fatalf("server name = %q", b.Server().Name())
	}
}

func TestFileConfigToConfigAndEnvKeys(t *testing.T) {
	fileCfg := FileConfig{
		Enabled:         true,
		ConfigFile:      "nats.conf",
		ReadyTimeout:    2 * time.Second,
		ShutdownTimeout: 3 * time.Second,
	}
	cfg := fileCfg.ToConfig()
	if cfg.ConfigFile != fileCfg.ConfigFile || cfg.ReadyTimeout != fileCfg.ReadyTimeout || cfg.ShutdownTimeout != fileCfg.ShutdownTimeout {
		t.Fatalf("ToConfig = %+v", cfg)
	}
	want := []string{
		"proxy_broker.enabled",
		"proxy_broker.config_file",
		"proxy_broker.ready_timeout",
		"proxy_broker.shutdown_timeout",
	}
	if got := FileConfigEnvKeys("proxy_broker"); !reflect.DeepEqual(got, want) {
		t.Fatalf("FileConfigEnvKeys = %v", got)
	}
	if got := FileConfigEnvKeys(".proxy_broker."); !reflect.DeepEqual(got, want) {
		t.Fatalf("FileConfigEnvKeys trimmed = %v", got)
	}
}

func TestStartReadyTimeoutPortIsStillOccupied(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	configFile := filepath.Join(t.TempDir(), "nats.conf")
	config := []byte("server_name: broker-busy-port-test\nlisten: 127.0.0.1:" + port + "\n")
	if err := os.WriteFile(configFile, config, 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	_, err = Start(context.Background(), Config{
		ConfigFile:      configFile,
		ReadyTimeout:    50 * time.Millisecond,
		ShutdownTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("Start error = nil, want busy port error")
	}
}
