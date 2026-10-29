// Package httpserver provides an explicitly started HTTP/HTTPS listener with
// optional, persistent ACME certificate management. WebSocket handlers are reused.
package httpserver

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// Mode selects how an HTTP entrypoint obtains its transport security.
type Mode string

const (
	ModePlain  Mode = "plain"
	ModeManual Mode = "manual"
	ModeACME   Mode = "acme"
)

// TLSConfig is decode-friendly configuration. The zero value preserves HTTP.
// CacheDir contains private ACME assets and must survive application upgrades.
type TLSConfig struct {
	Mode            Mode     `mapstructure:"mode"`
	Domains         []string `mapstructure:"domains"`
	CertFile        string   `mapstructure:"cert_file"`
	KeyFile         string   `mapstructure:"key_file"`
	CacheDir        string   `mapstructure:"cache_dir"`
	ACMEEnvironment string   `mapstructure:"acme_environment"`
	AgreeTerms      bool     `mapstructure:"agree_terms"`
}

// TLSConfigEnvKeys lists optional host-managed environment override keys.
func TLSConfigEnvKeys(prefix string) []string {
	prefix = strings.Trim(prefix, ".")
	keys := []string{"mode", "domains", "cert_file", "key_file", "cache_dir", "acme_environment", "agree_terms"}
	if prefix != "" {
		for i := range keys {
			keys[i] = prefix + "." + keys[i]
		}
	}
	return keys
}

// Validate rejects incomplete and conflicting modes without side effects.
func (cfg TLSConfig) Validate() error {
	_, err := normalizeConfig(cfg)
	return err
}

func normalizeConfig(cfg TLSConfig) (TLSConfig, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModePlain
	}
	cfg.CertFile, cfg.KeyFile = strings.TrimSpace(cfg.CertFile), strings.TrimSpace(cfg.KeyFile)
	cfg.CacheDir = strings.TrimSpace(cfg.CacheDir)
	cfg.ACMEEnvironment = strings.TrimSpace(cfg.ACMEEnvironment)
	switch cfg.Mode {
	case ModePlain:
		if len(cfg.Domains) != 0 || cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CacheDir != "" || cfg.ACMEEnvironment != "" || cfg.AgreeTerms {
			return TLSConfig{}, fmt.Errorf("http tls: plain mode cannot contain certificate settings")
		}
		return cfg, nil
	case ModeManual:
		if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CacheDir != "" || cfg.ACMEEnvironment != "" || cfg.AgreeTerms {
			return TLSConfig{}, fmt.Errorf("http tls: manual mode requires cert_file/key_file and no ACME settings")
		}
	case ModeACME:
		if cfg.CacheDir == "" || !cfg.AgreeTerms || cfg.CertFile != "" || cfg.KeyFile != "" {
			return TLSConfig{}, fmt.Errorf("http tls: acme mode requires cache_dir/agree_terms and no manual certificate")
		}
		if cfg.ACMEEnvironment == "" {
			cfg.ACMEEnvironment = "production"
		}
		if cfg.ACMEEnvironment != "production" && cfg.ACMEEnvironment != "staging" {
			return TLSConfig{}, fmt.Errorf("http tls: acme_environment must be production or staging")
		}
	default:
		return TLSConfig{}, fmt.Errorf("http tls: unknown mode %q", cfg.Mode)
	}
	if len(cfg.Domains) == 0 || len(cfg.Domains) > 100 {
		return TLSConfig{}, fmt.Errorf("http tls: between 1 and 100 explicit DNS names are required")
	}
	names := make([]string, 0, len(cfg.Domains))
	seen := make(map[string]bool, len(cfg.Domains))
	for _, raw := range cfg.Domains {
		name, err := normalizeDomain(raw)
		if err != nil {
			return TLSConfig{}, err
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	cfg.Domains = names
	return cfg, nil
}

func normalizeDomain(raw string) (string, error) {
	name, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), "."))
	if err != nil || len(name) > 253 || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
		return "", fmt.Errorf("http tls: invalid explicit DNS name %q", raw)
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("http tls: invalid explicit DNS name %q", raw)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("http tls: invalid explicit DNS name %q", raw)
			}
		}
	}
	return name, nil
}
