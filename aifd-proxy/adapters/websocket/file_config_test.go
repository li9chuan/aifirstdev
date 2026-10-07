package websocket

import "testing"

func TestFileConfigApply(t *testing.T) {
	base := Config{DuplicatePolicy: DuplicateKickOld}
	cfg, err := (FileConfig{
		MaxMessageBytes: 4096,
		DuplicatePolicy: DuplicateRejectNew,
		PublicTopics:    []string{"McPing"},
	}).Apply(base)
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}
	if cfg.MaxMessageBytes != 4096 || cfg.DuplicatePolicy != DuplicateRejectNew {
		t.Fatalf("config = %+v", cfg)
	}
	if len(cfg.PublicTopics) != 1 || cfg.PublicTopics[0] != "McPing" {
		t.Fatalf("public topics = %#v", cfg.PublicTopics)
	}
}

func TestFileConfigApplyRejectsInvalidPolicy(t *testing.T) {
	if _, err := (FileConfig{DuplicatePolicy: "replace"}).Apply(Config{}); err == nil {
		t.Fatal("Apply error = nil, want invalid policy error")
	}
}

func TestFileConfigApplyRejectsNegativeMaxMessageBytes(t *testing.T) {
	if _, err := (FileConfig{MaxMessageBytes: -1}).Apply(Config{}); err == nil {
		t.Fatal("Apply error = nil, want invalid max message bytes error")
	}
}

func TestFileConfigEnvKeys(t *testing.T) {
	keys := FileConfigEnvKeys("proxy")
	want := []string{
		"proxy.max_message_bytes",
		"proxy.duplicate_policy",
		"proxy.public_topics",
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
