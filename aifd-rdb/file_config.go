package rdb

import (
	"fmt"
	"strings"
	"time"

	redisgo "github.com/redis/go-redis/v9"
)

// RedisFileConfig is the decode-friendly Redis connection config.
type RedisFileConfig struct {
	Addrs        []string      `mapstructure:"addrs"`
	Password     string        `mapstructure:"password"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// RedisFileConfigEnvKeys returns environment override keys for RedisFileConfig under prefix.
func RedisFileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix,
		"addrs",
		"password",
		"dial_timeout",
		"read_timeout",
		"write_timeout",
	)
}

// RedisOptions converts file config into caller-owned Redis options.
func (cfg RedisFileConfig) RedisOptions() (*redisgo.UniversalOptions, error) {
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("rdb.redis.addrs is required")
	}
	return &redisgo.UniversalOptions{
		Addrs:        append([]string(nil), cfg.Addrs...),
		Password:     cfg.Password,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}, nil
}

// DBFileConfig is the decode-friendly final-intent persistence behavior config.
type DBFileConfig struct {
	MinSaveDelay          time.Duration `mapstructure:"min_save_delay"`
	MaxSaveDelay          time.Duration `mapstructure:"max_save_delay"`
	SaveStayMin           time.Duration `mapstructure:"save_stay_min"`
	MinRetryDelay         time.Duration `mapstructure:"min_retry_delay"`
	MaxRetryDelay         time.Duration `mapstructure:"max_retry_delay"`
	FailureFileDir        string        `mapstructure:"failure_file_dir"`
	StartupReplay         bool          `mapstructure:"startup_replay"`
	LargeSetPayloadBytes  int           `mapstructure:"large_set_payload_bytes"`
	LargeHashPayloadBytes int           `mapstructure:"large_hash_payload_bytes"`
}

// DBFileConfigEnvKeys returns environment override keys for DBFileConfig under prefix.
func DBFileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix,
		"min_save_delay",
		"max_save_delay",
		"save_stay_min",
		"min_retry_delay",
		"max_retry_delay",
		"failure_file_dir",
		"startup_replay",
		"large_set_payload_bytes",
		"large_hash_payload_bytes",
	)
}

// DBOptions converts file config into rdb Options.
func (cfg DBFileConfig) DBOptions() ([]Option, error) {
	opts := make([]Option, 0, 6)
	if cfg.MinSaveDelay != 0 || cfg.MaxSaveDelay != 0 {
		opts = append(opts, WithSaveDelay(cfg.MinSaveDelay, cfg.MaxSaveDelay))
	}
	if cfg.SaveStayMin != 0 {
		opts = append(opts, WithSaveStayMin(cfg.SaveStayMin))
	}
	if cfg.MinRetryDelay != 0 || cfg.MaxRetryDelay != 0 {
		opts = append(opts, WithRetryDelay(cfg.MinRetryDelay, cfg.MaxRetryDelay))
	}
	if cfg.FailureFileDir != "" {
		opts = append(opts, WithFailureFileDir(cfg.FailureFileDir))
	}
	if cfg.StartupReplay {
		opts = append(opts, WithStartupReplay())
	}
	if cfg.LargeSetPayloadBytes != 0 || cfg.LargeHashPayloadBytes != 0 {
		opts = append(opts, WithLargePayloadThresholds(cfg.LargeSetPayloadBytes, cfg.LargeHashPayloadBytes))
	}
	if _, err := applyOptions(opts); err != nil {
		return nil, err
	}
	return opts, nil
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
