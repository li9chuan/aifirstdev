package nats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileConfigToConfig(t *testing.T) {
	cfg, err := (FileConfig{
		Servers:             []string{"nats://127.0.0.1:4222"},
		Timeout:             3 * time.Second,
		Buffer:              8,
		LogDebug:            true,
		DefaultTimeout:      5 * time.Second,
		StatsWindow:         time.Minute,
		SubjectPrefix:       "RPC2.",
		ClientSubjectPrefix: "C.",
		StreamSubjectPrefix: "stream.",
	}).ToConfig("svc")
	if err != nil {
		t.Fatalf("ToConfig error = %v", err)
	}
	if cfg.Options.Name != "svc" || len(cfg.Options.Servers) != 1 || cfg.Options.Servers[0] != "nats://127.0.0.1:4222" {
		t.Fatalf("options = %+v", cfg.Options)
	}
	if cfg.Options.Timeout != 3*time.Second || cfg.Buffer != 8 || !cfg.LogDebug || cfg.DefaultTimeout != 5*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.SubjectPrefix != "RPC2." || cfg.ClientSubjectPrefix != "C." || cfg.StreamSubjectPrefix != "stream." {
		t.Fatalf("prefix config = %+v", cfg)
	}
}

func TestFileConfigToConfigRequiresServers(t *testing.T) {
	if _, err := (FileConfig{}).ToConfig("svc"); err == nil {
		t.Fatal("ToConfig error = nil, want missing servers error")
	}
}

func TestFileConfigToConfigAppliesCredentialsFile(t *testing.T) {
	credsFile := filepath.Join(t.TempDir(), "user.creds")
	if err := os.WriteFile(credsFile, []byte(testNATSCredentials), 0600); err != nil {
		t.Fatalf("WriteFile credentials error = %v", err)
	}
	cfg, err := (FileConfig{
		Servers:         []string{"nats://127.0.0.1:4222"},
		CredentialsFile: credsFile,
	}).ToConfig("svc")
	if err != nil {
		t.Fatalf("ToConfig error = %v", err)
	}
	if cfg.Options.UserJWT == nil {
		t.Fatal("UserJWT callback is nil, want credentials file option applied")
	}
}

const testNATSCredentials = `-----BEGIN NATS USER JWT-----
eyJ0eXAiOiJqd3QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.eyJqdGkiOiJHVDROVU5NRUY3Wk1XQ1JCWFZWVURLUVQ2WllQWjc3VzRKUlFYRDNMMjRIS1VKRUNRSDdRIiwiaWF0IjoxNTkwNzgxNTkzLCJpc3MiOiJBQURXTFRISUNWNFNVQUdGNkVLTlZFVzVCQlA3WVJESUJHV0dHSFo1SkJET1FZQTdHVUZNNkFRVSIsIm5hbWUiOiJPUEVSQVRPUiIsInN1YiI6IlVERTZXVEdMVFRQQ1JKUkpDS0JKUkdWTlpUTElWUjdMRUVFTFI0Q1lXV1dCS0pTN1hZSUtYRFVVIiwibmF0cyI6eyJwdWIiOnt9LCJzdWIiOnt9LCJ0eXBlIjoidXNlciIsInZlcnNpb24iOjJ9fQ.c_XQT04wEoVVNDRjPHeKwe17BOrSpQTcftwIbB7KoNEIz6peZCJDc4-J3emVepHofUOWy7IAo9TlLwYhuGHWAQ
------END NATS USER JWT------

-----BEGIN USER NKEY SEED-----
SUAOTBNEUHZDFJT3EUMELT7MQTP24JF3XVCXQNDSCU74G5IU6VAJBKH5LI
------END USER NKEY SEED------
`

func TestFileConfigToConfigRejectsPartialClientCert(t *testing.T) {
	if _, err := (FileConfig{
		Servers:     []string{"nats://127.0.0.1:4222"},
		TLSCertFile: "client.crt",
	}).ToConfig("svc"); err == nil {
		t.Fatal("ToConfig error = nil, want partial client cert error")
	}
	if _, err := (FileConfig{
		Servers:    []string{"nats://127.0.0.1:4222"},
		TLSKeyFile: "client.key",
	}).ToConfig("svc"); err == nil {
		t.Fatal("ToConfig error = nil, want partial client key error")
	}
}

func TestFileConfigToConfigReportsTLSFileErrors(t *testing.T) {
	if _, err := (FileConfig{
		Servers:   []string{"nats://127.0.0.1:4222"},
		TLSCAFile: filepath.Join(t.TempDir(), "missing-ca.pem"),
	}).ToConfig("svc"); err == nil {
		t.Fatal("ToConfig error = nil, want missing CA file error")
	}
	if _, err := (FileConfig{
		Servers:     []string{"nats://127.0.0.1:4222"},
		TLSCertFile: filepath.Join(t.TempDir(), "missing-client.crt"),
		TLSKeyFile:  filepath.Join(t.TempDir(), "missing-client.key"),
	}).ToConfig("svc"); err == nil {
		t.Fatal("ToConfig error = nil, want missing client cert file error")
	}
}

func TestFileConfigEnvKeys(t *testing.T) {
	keys := FileConfigEnvKeys("rpc.nats")
	want := []string{
		"rpc.nats.servers",
		"rpc.nats.timeout",
		"rpc.nats.credentials_file",
		"rpc.nats.tls_ca_file",
		"rpc.nats.tls_cert_file",
		"rpc.nats.tls_key_file",
		"rpc.nats.buffer",
		"rpc.nats.log_debug",
		"rpc.nats.default_timeout",
		"rpc.nats.stats_window",
		"rpc.nats.subject_prefix",
		"rpc.nats.client_subject_prefix",
		"rpc.nats.stream_subject_prefix",
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
