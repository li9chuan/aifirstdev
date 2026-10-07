package logging

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FileConfig is the decode-friendly logging config used by service entrypoints.
type FileConfig struct {
	Level          string `mapstructure:"level"`
	Stdout         bool   `mapstructure:"stdout"`
	Dir            string `mapstructure:"dir"`
	LocalPath      string `mapstructure:"local_path"`
	SourceService  string `mapstructure:"source_service"`
	SourceInstance string `mapstructure:"source_instance"`
}

// FileConfigEnvKeys returns environment override keys for FileConfig under prefix.
func FileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix,
		"level",
		"stdout",
		"dir",
		"local_path",
		"source_service",
		"source_instance",
	)
}

// ToConfig converts file config into the runtime logging Config.
func (cfg FileConfig) ToConfig() (Config, error) {
	level, err := parseFileLevel(cfg.Level)
	if err != nil {
		return Config{}, err
	}
	out := Config{
		LocalPath:      cfg.LocalPath,
		AlsoStdout:     cfg.Stdout,
		Level:          level,
		SourceService:  cfg.SourceService,
		SourceInstance: cfg.SourceInstance,
	}
	if out.LocalPath == "" && cfg.Dir != "" {
		service := out.SourceService
		if service == "" {
			service = defaultServiceName
		}
		out.LocalPath = filepath.Join(cfg.Dir, sanitizePathPart(service)+".log")
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
