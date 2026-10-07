package luban

import (
	"context"
	"errors"
	"testing"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

type testTables struct {
	Count int
}

func TestLoaderBuildsTypedBundle(t *testing.T) {
	loader := Loader[*testTables]{
		BuildBundle: func(context.Context) (*config.Bundle[*testTables], error) {
			return &config.Bundle[*testTables]{
				Version: "v1",
				Data:    &testTables{Count: 1},
				Manifest: config.Manifest{
					DataHash: "data:v1",
					Tables: map[string]config.TableManifest{
						"Counter": {Name: "Counter", RowCount: 1, Hash: "counter:v1"},
					},
				},
			}, nil
		},
	}

	bundle, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if bundle.Version != "v1" || bundle.Data.Count != 1 {
		t.Fatalf("bundle = %+v, want v1 count 1", bundle)
	}
	if bundle.Manifest.Tables["Counter"].Hash != "counter:v1" {
		t.Fatalf("manifest = %+v", bundle.Manifest)
	}
}

func TestLoaderRejectsNilBundleBuilder(t *testing.T) {
	loader := Loader[*testTables]{}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("Load error = nil, want nil builder error")
	}
}

func TestLoaderRejectsNilBundle(t *testing.T) {
	loader := Loader[*testTables]{
		BuildBundle: func(context.Context) (*config.Bundle[*testTables], error) {
			return nil, nil
		},
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("Load error = nil, want nil bundle error")
	}
}

func TestLoaderReturnsBundleBuilderError(t *testing.T) {
	wantErr := errors.New("generate tables")
	loader := Loader[*testTables]{
		BuildBundle: func(context.Context) (*config.Bundle[*testTables], error) {
			return nil, wantErr
		},
	}
	if _, err := loader.Load(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Load error = %v, want %v", err, wantErr)
	}
}
