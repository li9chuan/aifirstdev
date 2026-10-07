package memory

import (
	"context"
	"testing"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

type testConfig struct {
	Limit int
}

func TestLoaderReturnsCurrentBundle(t *testing.T) {
	first := testBundle("v1", 1)
	second := testBundle("v2", 2)
	loader := NewLoader(first)

	got, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load first error = %v", err)
	}
	if got != first || got.Data.Limit != 1 {
		t.Fatalf("first bundle = %+v, want original pointer with limit 1", got)
	}

	loader.SetBundle(second)
	got, err = loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load second error = %v", err)
	}
	if got != second || got.Data.Limit != 2 {
		t.Fatalf("second bundle = %+v, want original pointer with limit 2", got)
	}
}

func TestLoaderRejectsNilBundle(t *testing.T) {
	loader := NewLoader[testConfig](nil)
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("Load error = nil, want nil bundle error")
	}
}

func testBundle(version string, limit int) *config.Bundle[testConfig] {
	return &config.Bundle[testConfig]{
		Version: version,
		Data:    testConfig{Limit: limit},
		Manifest: config.Manifest{
			DataHash: version,
		},
	}
}
