package nats

import (
	"fmt"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// FileConfig is the decode-friendly NATS RPC config used by service entrypoints.
type FileConfig struct {
	Servers             []string      `mapstructure:"servers"`
	Timeout             time.Duration `mapstructure:"timeout"`
	CredentialsFile     string        `mapstructure:"credentials_file"`
	TLSCAFile           string        `mapstructure:"tls_ca_file"`
	TLSCertFile         string        `mapstructure:"tls_cert_file"`
	TLSKeyFile          string        `mapstructure:"tls_key_file"`
	Buffer              int           `mapstructure:"buffer"`
	LogDebug            bool          `mapstructure:"log_debug"`
	DefaultTimeout      time.Duration `mapstructure:"default_timeout"`
	StatsWindow         time.Duration `mapstructure:"stats_window"`
	SubjectPrefix       string        `mapstructure:"subject_prefix"`
	ClientSubjectPrefix string        `mapstructure:"client_subject_prefix"`
	StreamSubjectPrefix string        `mapstructure:"stream_subject_prefix"`
}

// FileConfigEnvKeys returns environment override keys for FileConfig under prefix.
func FileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix,
		"servers",
		"timeout",
		"credentials_file",
		"tls_ca_file",
		"tls_cert_file",
		"tls_key_file",
		"buffer",
		"log_debug",
		"default_timeout",
		"stats_window",
		"subject_prefix",
		"client_subject_prefix",
		"stream_subject_prefix",
	)
}

// ToConfig converts file config into the NATS RPC adapter Config.
func (cfg FileConfig) ToConfig(serviceName string) (Config, error) {
	if len(cfg.Servers) == 0 {
		return Config{}, fmt.Errorf("rpc.nats.servers is required")
	}
	opts := natsgo.GetDefaultOptions()
	opts.Servers = append([]string(nil), cfg.Servers...)
	opts.Name = serviceName
	if cfg.Timeout > 0 {
		opts.Timeout = cfg.Timeout
	}
	if err := cfg.applyClientOptions(&opts); err != nil {
		return Config{}, err
	}
	return Config{
		Options:             opts,
		Buffer:              cfg.Buffer,
		LogDebug:            cfg.LogDebug,
		DefaultTimeout:      cfg.DefaultTimeout,
		StatsWindow:         cfg.StatsWindow,
		SubjectPrefix:       cfg.SubjectPrefix,
		ClientSubjectPrefix: cfg.ClientSubjectPrefix,
		StreamSubjectPrefix: cfg.StreamSubjectPrefix,
	}, nil
}

func (cfg FileConfig) applyClientOptions(opts *natsgo.Options) error {
	if path := strings.TrimSpace(cfg.CredentialsFile); path != "" {
		if err := natsgo.UserCredentials(path)(opts); err != nil {
			return fmt.Errorf("rpc.nats.credentials_file: %w", err)
		}
	}
	if path := strings.TrimSpace(cfg.TLSCAFile); path != "" {
		if err := natsgo.RootCAs(path)(opts); err != nil {
			return fmt.Errorf("rpc.nats.tls_ca_file: %w", err)
		}
	}
	certFile := strings.TrimSpace(cfg.TLSCertFile)
	keyFile := strings.TrimSpace(cfg.TLSKeyFile)
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("rpc.nats.tls_cert_file and rpc.nats.tls_key_file must be set together")
	}
	if certFile != "" {
		if err := natsgo.ClientCert(certFile, keyFile)(opts); err != nil {
			return fmt.Errorf("rpc.nats.tls_cert_file/tls_key_file: %w", err)
		}
	}
	return nil
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
