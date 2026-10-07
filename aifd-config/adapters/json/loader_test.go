package json

import (
	"context"
	"testing"
)

type testConfig struct {
	Limit int `json:"limit"`
}

func TestParseBundleDecodesTypedDataAndManifest(t *testing.T) {
	bundle, err := ParseBundle[testConfig]([]byte(`{
  "version": "v1",
  "manifest": {
    "data_hash": "data:v1",
    "schema_hash": "schema:v1",
    "profile": "test",
    "tables": {
      "Counter": {"name": "Counter", "row_count": 1, "hash": "counter:v1"}
    },
    "sources": {
      "counter.json": {"name": "counter.json", "path": "testdata/counter.json", "hash": "source:v1", "kind": "json"}
    },
    "labels": {"env": "test"}
  },
  "data": {"limit": 10}
}`))
	if err != nil {
		t.Fatalf("ParseBundle error = %v", err)
	}
	if bundle.Version != "v1" || bundle.Data.Limit != 10 {
		t.Fatalf("bundle = %+v, want version v1 limit 10", bundle)
	}
	table := bundle.Manifest.Tables["Counter"]
	if table.Hash != "counter:v1" || table.RowCount != 1 {
		t.Fatalf("table manifest = %+v", table)
	}
	if bundle.Manifest.Sources["counter.json"].Path != "testdata/counter.json" {
		t.Fatalf("sources = %+v", bundle.Manifest.Sources)
	}
	if bundle.Manifest.Labels["env"] != "test" {
		t.Fatalf("labels = %+v", bundle.Manifest.Labels)
	}
}

func TestLoaderLoadsTypedBundle(t *testing.T) {
	loader := NewLoader[testConfig]([]byte(`{"version":"v1","data":{"limit":5}}`))
	bundle, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if bundle.Version != "v1" || bundle.Data.Limit != 5 {
		t.Fatalf("bundle = %+v, want version v1 limit 5", bundle)
	}
}
