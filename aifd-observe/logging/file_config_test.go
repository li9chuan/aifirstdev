package logging

import (
	"path/filepath"
	"testing"
)

func TestFileConfigToConfigParsesLevel(t *testing.T) {
	cases := map[string]Level{
		"":        LevelInfo,
		"debug":   LevelDebug,
		"info":    LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
	}
	for input, want := range cases {
		cfg, err := (FileConfig{Level: input}).ToConfig()
		if err != nil {
			t.Fatalf("ToConfig(%q) error = %v", input, err)
		}
		if cfg.Level != want {
			t.Fatalf("ToConfig(%q) level = %v, want %v", input, cfg.Level, want)
		}
	}
}

func TestFileConfigToConfigRejectsInvalidLevel(t *testing.T) {
	if _, err := (FileConfig{Level: "trace"}).ToConfig(); err == nil {
		t.Fatal("ToConfig error = nil, want invalid level error")
	}
}

func TestFileConfigToConfigBuildsLocalPathFromDir(t *testing.T) {
	cfg, err := (FileConfig{Dir: "logs", SourceService: "proxy:1"}).ToConfig()
	if err != nil {
		t.Fatalf("ToConfig error = %v", err)
	}
	want := filepath.Join("logs", "proxy_1.log")
	if cfg.LocalPath != want {
		t.Fatalf("LocalPath = %q, want %q", cfg.LocalPath, want)
	}
}

func TestFileConfigEnvKeys(t *testing.T) {
	keys := FileConfigEnvKeys("log")
	want := []string{
		"log.level",
		"log.stdout",
		"log.dir",
		"log.local_path",
		"log.source_service",
		"log.source_instance",
	}
	if len(keys) != len(want) {
		t.Fatalf("len(keys) = %d, want %d: %#v", len(keys), len(want), keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys[%d] = %q, want %q", i, keys[i], want[i])
		}
	}
}
