package rdb

import (
	"testing"
	"time"
)

func TestRedisFileConfigRedisOptions(t *testing.T) {
	opts, err := (RedisFileConfig{
		Addrs:        []string{"127.0.0.1:6379"},
		Password:     "secret",
		DialTimeout:  time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 3 * time.Second,
	}).RedisOptions()
	if err != nil {
		t.Fatalf("RedisOptions error = %v", err)
	}
	if len(opts.Addrs) != 1 || opts.Addrs[0] != "127.0.0.1:6379" || opts.Password != "secret" {
		t.Fatalf("options = %+v", opts)
	}
	if opts.DialTimeout != time.Second || opts.ReadTimeout != 2*time.Second || opts.WriteTimeout != 3*time.Second {
		t.Fatalf("timeouts = %+v", opts)
	}
}

func TestRedisFileConfigRequiresAddrs(t *testing.T) {
	if _, err := (RedisFileConfig{}).RedisOptions(); err == nil {
		t.Fatal("RedisOptions error = nil, want missing addrs error")
	}
}

func TestRedisFileConfigEnvKeys(t *testing.T) {
	keys := RedisFileConfigEnvKeys("rdb.redis")
	want := []string{
		"rdb.redis.addrs",
		"rdb.redis.password",
		"rdb.redis.dial_timeout",
		"rdb.redis.read_timeout",
		"rdb.redis.write_timeout",
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

func TestDBFileConfigDBOptions(t *testing.T) {
	opts, err := (DBFileConfig{
		MinSaveDelay:          time.Millisecond,
		MaxSaveDelay:          time.Second,
		SaveStayMin:           3 * time.Millisecond,
		MinRetryDelay:         2 * time.Millisecond,
		MaxRetryDelay:         2 * time.Second,
		FailureFileDir:        "failed",
		StartupReplay:         true,
		LargeSetPayloadBytes:  1024,
		LargeHashPayloadBytes: 2048,
	}).DBOptions()
	if err != nil {
		t.Fatalf("DBOptions error = %v", err)
	}
	if len(opts) != 6 {
		t.Fatalf("len(options) = %d, want 6", len(opts))
	}
	applied, err := applyDBOptions(opts)
	if err != nil {
		t.Fatalf("applyDBOptions error = %v", err)
	}
	if applied.saveStayMin != 3*time.Millisecond {
		t.Fatalf("saveStayMin = %v, want 3ms", applied.saveStayMin)
	}
}

func TestDBFileConfigValidatesDelay(t *testing.T) {
	if _, err := (DBFileConfig{MinSaveDelay: time.Second}).DBOptions(); err == nil {
		t.Fatal("DBOptions error = nil, want invalid save delay error")
	}
}

func TestDBFileConfigValidatesSaveStayMin(t *testing.T) {
	if _, err := (DBFileConfig{SaveStayMin: -time.Nanosecond}).DBOptions(); err == nil {
		t.Fatal("DBOptions error = nil, want invalid save stay min error")
	}
}

func TestDBFileConfigEnvKeys(t *testing.T) {
	keys := DBFileConfigEnvKeys("rdb.db")
	want := []string{
		"rdb.db.min_save_delay",
		"rdb.db.max_save_delay",
		"rdb.db.save_stay_min",
		"rdb.db.min_retry_delay",
		"rdb.db.max_retry_delay",
		"rdb.db.failure_file_dir",
		"rdb.db.startup_replay",
		"rdb.db.large_set_payload_bytes",
		"rdb.db.large_hash_payload_bytes",
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
