package httpserver

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caddyserver/certmagic"
)

func TestCertificateBundleSurvivesInterruptedWrite(t *testing.T) {
	ctx := context.Background()
	disk := &certmagic.FileStorage{Path: t.TempDir()}
	store := newAssetStorage(disk, func(string) {})
	base := "certificates/test-ca/api.example.test/api.example.test"
	for _, ext := range []string{".key", ".crt", ".json"} {
		if err := store.Store(ctx, base+ext, []byte("old"+ext)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a process stopping after only the key and certificate were staged.
	_ = store.Store(ctx, base+".key", []byte("uncommitted key"))
	_ = store.Store(ctx, base+".crt", []byte("uncommitted certificate"))
	restarted := newAssetStorage(disk, func(string) {})
	for _, ext := range []string{".key", ".crt", ".json"} {
		value, err := restarted.Load(ctx, base+ext)
		if err != nil || string(value) != "old"+ext {
			t.Fatalf("interrupted renewal changed %s: %q %v", ext, value, err)
		}
		info, err := restarted.Stat(ctx, base+ext)
		if err != nil || info.Key != base+ext || !info.IsTerminal || info.Size != int64(len(value)) {
			t.Fatalf("virtual asset Stat = %+v, %v", info, err)
		}
	}
	keys, err := restarted.List(ctx, filepath.ToSlash(filepath.Dir(base)), true)
	if err != nil || len(keys) != 3 {
		t.Fatalf("virtual certificate listing = %v, %v", keys, err)
	}
	for _, key := range keys {
		if strings.Contains(key, bundleSuffix) || !restarted.Exists(ctx, key) {
			t.Fatalf("invalid virtual asset %s", key)
		}
	}
	// Emulate the library's rollback calls after a failed later write.
	_ = store.Delete(ctx, base+".crt")
	_ = store.Delete(ctx, base+".key")
	if value, _ := store.Load(ctx, base+".key"); string(value) != "old.key" {
		t.Fatal("rollback deleted the previously committed private key")
	}
	if err := restarted.Delete(ctx, base+".crt"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Load(ctx, base+".key"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("explicit delete did not remove the committed certificate resource")
	}
}

func TestCertificateMetadataRefreshPreservesPendingRenewal(t *testing.T) {
	ctx := context.Background()
	disk := &certmagic.FileStorage{Path: t.TempDir()}
	store := newAssetStorage(disk, func(string) {})
	base := "certificates/test-ca/api.example.test/api.example.test"
	oldMetadata := []byte(`{"issuer_data":{"url":"https://ca.test/cert/old","renewal_info":{"_retryAfter":"2026-10-29T00:00:00Z"}}}`)
	updatedMetadata := []byte(`{"issuer_data":{"url":"https://ca.test/cert/old","renewal_info":{"_retryAfter":"2026-10-30T00:00:00Z"}}}`)
	newMetadata := []byte(`{"issuer_data":{"url":"https://ca.test/cert/new"}}`)
	for _, asset := range []struct {
		ext   string
		value []byte
	}{{".key", []byte("old key")}, {".crt", []byte("old certificate")}, {".json", oldMetadata}} {
		if err := store.Store(ctx, base+asset.ext, asset.value); err != nil {
			t.Fatal(err)
		}
	}
	// ARI writes only metadata, without going through the key/cert transaction.
	if err := store.Store(ctx, base+".json", updatedMetadata); err != nil {
		t.Fatal(err)
	}
	restarted := newAssetStorage(disk, func(string) {})
	if got, err := restarted.Load(ctx, base+".json"); err != nil || string(got) != string(updatedMetadata) {
		t.Fatalf("metadata refresh did not survive restart: %s %v", got, err)
	}
	// An ARI refresh for the old certificate can interleave with a replacement.
	if err := store.Store(ctx, base+".key", []byte("new key")); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, base+".crt", []byte("new certificate")); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, base+".json", oldMetadata); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(ctx, base+".crt"); string(got) != "old certificate" {
		t.Fatal("ARI refresh committed the staged replacement with old metadata")
	}
	if err := store.Store(ctx, base+".json", newMetadata); err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, base+".json", updatedMetadata); err == nil {
		t.Fatal("stale ARI update overwrote the replacement's metadata")
	}
	for ext, want := range map[string]string{".key": "new key", ".crt": "new certificate", ".json": string(newMetadata)} {
		if got, err := restarted.Load(ctx, base+ext); err != nil || string(got) != want {
			t.Fatalf("replacement %s = %s, %v; want %s", ext, got, err, want)
		}
	}
}

func TestCertificateMetadataWriteFailurePreservesCommittedBundle(t *testing.T) {
	ctx := context.Background()
	disk := &metadataFaultStorage{Storage: &certmagic.FileStorage{Path: t.TempDir()}}
	var event string
	store := newAssetStorage(disk, func(code string) { event = code })
	base := "certificates/test-ca/api.example.test/api.example.test"
	metadata := []byte(`{"issuer_data":{"url":"https://ca.test/cert/old"}}`)
	for _, ext := range []string{".key", ".crt", ".json"} {
		value := []byte("old" + ext)
		if ext == ".json" {
			value = metadata
		}
		if err := store.Store(ctx, base+ext, value); err != nil {
			t.Fatal(err)
		}
	}
	disk.fail = true
	updated := []byte(`{"issuer_data":{"url":"https://ca.test/cert/old","renewal_info":{"_retryAfter":"2026-10-30T00:00:00Z"}}}`)
	if err := store.Store(ctx, base+".json", updated); err == nil || event != "storage_failed" {
		t.Fatalf("metadata write failure not reported: %v, event %q", err, event)
	}
	restarted := newAssetStorage(disk, func(string) {})
	for ext, want := range map[string]string{".key": "old.key", ".crt": "old.crt", ".json": string(metadata)} {
		if got, err := restarted.Load(ctx, base+ext); err != nil || string(got) != want {
			t.Fatalf("failed metadata commit changed %s: %s %v", ext, got, err)
		}
	}
}

type metadataFaultStorage struct {
	certmagic.Storage
	fail bool
}

func (s *metadataFaultStorage) Store(ctx context.Context, key string, value []byte) error {
	if s.fail {
		return errors.New("synthetic metadata disk-full failure")
	}
	return s.Storage.Store(ctx, key, value)
}
