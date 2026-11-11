package logging

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// FileConfig is the decode-friendly logging config used by service entrypoints.
type FileConfig struct {
	Level          string          `mapstructure:"level"`
	Stdout         bool            `mapstructure:"stdout"`
	Dir            string          `mapstructure:"dir"`
	LocalPath      string          `mapstructure:"local_path"`
	SourceService  string          `mapstructure:"source_service"`
	SourceInstance string          `mapstructure:"source_instance"`
	Rotation       RotationConfig  `mapstructure:"rotation"`
	Stat           *StatFileConfig `mapstructure:"stat"`
}

// StatFileConfig configures local typed Stat output. Applications inject Forward
// after ToConfig; file config does not define transports or receiving services.
type StatFileConfig struct {
	LocalPath           string         `mapstructure:"local_path"`
	Rotation            RotationConfig `mapstructure:"rotation"`
	ForwardQueueSize    int            `mapstructure:"forward_queue_size"`
	ForwardTimeout      time.Duration  `mapstructure:"forward_timeout"`
	ForwardDrainTimeout time.Duration  `mapstructure:"forward_drain_timeout"`
}

// FileConfigEnvKeys returns environment override keys for FileConfig under prefix.
func FileConfigEnvKeys(prefix string) []string {
	keys := prefixedKeys(prefix,
		"level",
		"stdout",
		"dir",
		"local_path",
		"source_service",
		"source_instance",
		"stat.local_path",
		"stat.forward_queue_size",
		"stat.forward_timeout",
		"stat.forward_drain_timeout",
	)
	for _, base := range []string{"rotation.", "stat.rotation."} {
		keys = append(keys, prefixedKeys(prefix,
			base+"max_size_mb", base+"max_backups", base+"max_age_days",
			base+"compress", base+"disable_daily", base+"utc",
		)...)
	}
	return keys
}

// ToConfig converts file config into the runtime logging Config.
func (cfg FileConfig) ToConfig() (Config, error) {
	level, err := parseFileLevel(cfg.Level)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Rotation.validate(); err != nil {
		return Config{}, err
	}
	out := Config{
		LocalPath:      cfg.LocalPath,
		AlsoStdout:     cfg.Stdout,
		Level:          level,
		SourceService:  cfg.SourceService,
		SourceInstance: cfg.SourceInstance,
		Rotation:       cfg.Rotation,
	}
	if out.LocalPath == "" && cfg.Dir != "" {
		service := out.SourceService
		if service == "" {
			service = defaultServiceName
		}
		out.LocalPath = filepath.Join(cfg.Dir, sanitizePathPart(service)+".log")
	}
	if cfg.Stat != nil {
		stat, err := (StatConfig{
			LocalPath:           cfg.Stat.LocalPath,
			Rotation:            cfg.Stat.Rotation,
			ForwardQueueSize:    cfg.Stat.ForwardQueueSize,
			ForwardTimeout:      cfg.Stat.ForwardTimeout,
			ForwardDrainTimeout: cfg.Stat.ForwardDrainTimeout,
		}).withDefaults()
		if err != nil {
			return Config{}, err
		}
		out.Stat = &stat
	}
	return out, nil
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

func parseFileLevel(value string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("logging level %q is invalid", value)
	}
}
