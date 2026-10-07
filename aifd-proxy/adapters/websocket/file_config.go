package websocket

import (
	"fmt"
	"strings"
)

// FileConfig is the decode-friendly websocket proxy behavior config.
type FileConfig struct {
	MaxMessageBytes int64           `mapstructure:"max_message_bytes"`
	DuplicatePolicy DuplicatePolicy `mapstructure:"duplicate_policy"`
	PublicTopics    []string        `mapstructure:"public_topics"`
}

// FileConfigEnvKeys returns environment override keys for FileConfig under prefix.
func FileConfigEnvKeys(prefix string) []string {
	return prefixedKeys(prefix,
		"max_message_bytes",
		"duplicate_policy",
		"public_topics",
	)
}

// Apply overlays file config onto an existing websocket Config.
func (cfg FileConfig) Apply(base Config) (Config, error) {
	if cfg.MaxMessageBytes < 0 {
		return Config{}, fmt.Errorf("proxy.max_message_bytes must not be negative")
	}
	if cfg.MaxMessageBytes > 0 {
		base.MaxMessageBytes = cfg.MaxMessageBytes
	}
	if cfg.DuplicatePolicy != "" {
		switch cfg.DuplicatePolicy {
		case DuplicateAllow, DuplicateKickOld, DuplicateRejectNew:
			base.DuplicatePolicy = cfg.DuplicatePolicy
		default:
			return Config{}, fmt.Errorf("proxy.duplicate_policy %q is invalid", cfg.DuplicatePolicy)
		}
	}
	if cfg.PublicTopics != nil {
		base.PublicTopics = append([]string(nil), cfg.PublicTopics...)
	}
	return base, nil
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
