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
		"log.stat.local_path",
		"log.stat.forward_queue_size",
		"log.stat.forward_timeout",
		"log.stat.forward_drain_timeout",
		"log.rotation.max_size_mb",
		"log.rotation.max_backups",
		"log.rotation.max_age_days",
		"log.rotation.compress",
		"log.rotation.disable_daily",
		"log.rotation.utc",
		"log.stat.rotation.max_size_mb",
		"log.stat.rotation.max_backups",
		"log.stat.rotation.max_age_days",
		"log.stat.rotation.compress",
		"log.stat.rotation.disable_daily",
		"log.stat.rotation.utc",
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

func TestFileConfigConvertsIndependentRotationAndRejectsInvalidLimits(t *testing.T) {
	cfg, err := (FileConfig{
		Rotation: RotationConfig{MaxSizeMB: 12, MaxBackups: 4, MaxAgeDays: 7, Compress: true, UTC: true},
		Stat:     &StatFileConfig{LocalPath: "stat.log", Rotation: RotationConfig{MaxSizeMB: 34, DisableDaily: true}},
	}).ToConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rotation.MaxSizeMB != 12 || cfg.Rotation.MaxBackups != 4 || cfg.Rotation.MaxAgeDays != 7 || !cfg.Rotation.Compress || !cfg.Rotation.UTC || cfg.Stat.Rotation.MaxSizeMB != 34 || !cfg.Stat.Rotation.DisableDaily {
		t.Fatalf("config = %+v / %+v", cfg.Rotation, cfg.Stat.Rotation)
	}
	for _, invalid := range []RotationConfig{{MaxSizeMB: -1}, {MaxBackups: -1}, {MaxAgeDays: -1}} {
		if _, err := (FileConfig{Rotation: invalid}).ToConfig(); err == nil {
			t.Fatalf("ordinary accepts %+v", invalid)
		}
		if _, err := (FileConfig{Stat: &StatFileConfig{LocalPath: "stat.log", Rotation: invalid}}).ToConfig(); err == nil {
			t.Fatalf("Stat accepts %+v", invalid)
		}
	}
}
