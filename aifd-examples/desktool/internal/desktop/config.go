package desktop

import (
	"os"
	"strings"
)

const (
	modeEmbedded = "embedded"
	modeRemote   = "remote"
)

func resolveConfig(configs ...Config) Config {
	cfg := Config{
		Mode:        getenv("DESKTOOL_MODE", modeEmbedded),
		ProxyURL:    getenv("DESKTOOL_PROXY_URL", "http://127.0.0.1:19082"),
		Version:     getenv("DESKTOOL_VERSION", currentVersion),
		ManifestURL: getenv("DESKTOOL_UPDATE_MANIFEST_URL", updateManifestURL),
	}
	if len(configs) > 0 {
		input := configs[0]
		if input.Mode != "" {
			cfg.Mode = input.Mode
		}
		if input.ProxyURL != "" {
			cfg.ProxyURL = input.ProxyURL
		}
		if input.Version != "" {
			cfg.Version = input.Version
		}
		if input.ManifestURL != "" {
			cfg.ManifestURL = input.ManifestURL
		}
		if input.Backend != nil {
			cfg.Backend = input.Backend
		}
	}
	if cfg.Backend == nil {
		switch strings.ToLower(cfg.Mode) {
		case modeRemote:
			cfg.Backend = newRemoteBackendClient(cfg.ProxyURL)
		default:
			cfg.Backend = newEmbeddedBackendClient()
		}
	}
	return cfg
}

func getenv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
