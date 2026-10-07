package broker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

const (
	defaultReadyTimeout    = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// FileConfig is the decode-friendly embedded NATS broker config for proxy entrypoints.
type FileConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	ConfigFile      string        `mapstructure:"config_file"`
	ReadyTimeout    time.Duration `mapstructure:"ready_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// FileConfigEnvKeys returns environment override keys for FileConfig under prefix.
func FileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix, "enabled", "config_file", "ready_timeout", "shutdown_timeout")
}

// ToConfig converts file config into the embedded broker runtime config.
func (cfg FileConfig) ToConfig() Config {
	return Config{
		ConfigFile:      cfg.ConfigFile,
		ReadyTimeout:    cfg.ReadyTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}
}

// Config controls an embedded NATS server owned by a proxy entrypoint.
type Config struct {
	ConfigFile      string
	Options         *server.Options
	ReadyTimeout    time.Duration
	ShutdownTimeout time.Duration
}

// Broker owns an embedded NATS server lifecycle for a proxy-broker process.
type Broker struct {
	srv             *server.Server
	shutdownTimeout time.Duration

	closeOnce sync.Once
	closeErr  error
}

// Start creates and starts an embedded NATS server, then waits until it is ready.
func Start(ctx context.Context, cfg Config) (*Broker, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	applyDefaults(&cfg)
	opts, err := buildOptions(cfg)
	if err != nil {
		return nil, err
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("proxy nats broker new server: %w", err)
	}
	srv.Start()
	if err := waitReady(ctx, srv, cfg.ReadyTimeout); err != nil {
		srv.Shutdown()
		srv.WaitForShutdown()
		return nil, err
	}
	return &Broker{srv: srv, shutdownTimeout: cfg.ShutdownTimeout}, nil
}

// Server returns the embedded NATS server instance for advanced diagnostics.
func (b *Broker) Server() *server.Server {
	if b == nil {
		return nil
	}
	return b.srv
}

// ClientURL returns the TCP client URL advertised by the embedded NATS server.
func (b *Broker) ClientURL() string {
	if b == nil || b.srv == nil {
		return ""
	}
	return b.srv.ClientURL()
}

// InProcessServer returns a provider for nats.go in-process client connections.
func (b *Broker) InProcessServer() natsgo.InProcessConnProvider {
	if b == nil {
		return nil
	}
	return b.srv
}

// Close shuts down the embedded NATS server and waits for it to stop.
func (b *Broker) Close(ctx context.Context) error {
	if b == nil || b.srv == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		if b.shutdownTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, b.shutdownTimeout)
			defer cancel()
		}
		b.srv.Shutdown()
		done := make(chan struct{})
		go func() {
			b.srv.WaitForShutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			b.closeErr = fmt.Errorf("proxy nats broker shutdown: %w", ctx.Err())
		}
	})
	return b.closeErr
}

func applyDefaults(cfg *Config) {
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = defaultReadyTimeout
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
}

func buildOptions(cfg Config) (*server.Options, error) {
	cfg.ConfigFile = strings.TrimSpace(cfg.ConfigFile)
	if cfg.Options == nil && cfg.ConfigFile == "" {
		return nil, fmt.Errorf("proxy nats broker config_file or options is required")
	}
	var opts *server.Options
	if cfg.Options != nil {
		opts = cfg.Options.Clone()
	}
	if cfg.ConfigFile != "" {
		if opts == nil {
			parsed, err := server.ProcessConfigFile(cfg.ConfigFile)
			if err != nil {
				return nil, fmt.Errorf("proxy nats broker config %q: %w", cfg.ConfigFile, err)
			}
			opts = parsed
		} else if err := opts.ProcessConfigFile(cfg.ConfigFile); err != nil {
			return nil, fmt.Errorf("proxy nats broker config %q: %w", cfg.ConfigFile, err)
		}
	}
	if opts == nil {
		opts = &server.Options{}
	}
	opts.NoSigs = true
	return opts, nil
}

func waitReady(ctx context.Context, srv *server.Server, timeout time.Duration) error {
	ready := make(chan bool, 1)
	go func() {
		ready <- srv.ReadyForConnections(timeout)
	}()
	select {
	case ok := <-ready:
		if !ok {
			return fmt.Errorf("proxy nats broker not ready within %s", timeout)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("proxy nats broker start canceled: %w", ctx.Err())
	}
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
