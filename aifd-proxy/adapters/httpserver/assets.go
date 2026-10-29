package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/caddyserver/certmagic"
)

const bundleSuffix = ".aifd-bundle"

// assetStorage commits the library's key/cert/metadata writes as one atomic
// FileStorage object. CertMagic's generic storeTx otherwise deletes earlier
// writes on failure, including a previously working private key during renewal.
// Only committed bundles are persistent truth; an interrupted staged write leaves
// the old bundle intact. ACME accounts, locks and other assets pass through.
type assetStorage struct {
	certmagic.Storage
	mu      sync.Mutex
	pending map[string]*certificateBundle
	event   func(string)
}

type certificateBundle struct {
	Version     int    `json:"version"`
	Key         []byte `json:"key"`
	Certificate []byte `json:"certificate"`
	Metadata    []byte `json:"metadata"`
}

func newAssetStorage(base certmagic.Storage, event func(string)) *assetStorage {
	return &assetStorage{Storage: base, pending: make(map[string]*certificateBundle), event: event}
}

func certificateAsset(key string) (base, ext string, ok bool) {
	ext = path.Ext(key)
	if !strings.HasPrefix(key, "certificates/") || ext != ".key" && ext != ".crt" && ext != ".json" {
		return "", "", false
	}
	return strings.TrimSuffix(key, ext), ext, true
}

func (s *assetStorage) Store(ctx context.Context, key string, value []byte) error {
	base, ext, asset := certificateAsset(key)
	if !asset {
		return s.Storage.Store(ctx, key, value)
	}
	// This mutex serializes rare certificate asset writes, never HTTP/WS requests.
	s.mu.Lock()
	defer s.mu.Unlock()
	if ext == ".key" {
		s.pending[base] = &certificateBundle{Version: 1, Key: append([]byte(nil), value...)}
		return nil
	}
	stage := s.pending[base]
	if ext == ".json" {
		// ARI refreshes update metadata without writing a key or certificate. Use
		// the ACME certificate resource URL to keep an old ARI update separate
		// from a renewal that has already staged its replacement key/cert pair.
		committed, err := s.loadBundle(ctx, base)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil {
			previousURL := metadataCertificateURL(committed.Metadata)
			if previousURL != "" && previousURL == metadataCertificateURL(value) {
				committed.Metadata = value
				return s.storeBundle(ctx, base, committed)
			}
		}
		if stage == nil {
			return fmt.Errorf("certificate metadata update must match the committed certificate")
		}
	}
	if stage == nil || len(stage.Key) == 0 {
		return fmt.Errorf("certificate asset write must start with a private key")
	}
	if ext == ".crt" {
		stage.Certificate = append([]byte(nil), value...)
		return nil
	}
	if len(stage.Certificate) == 0 {
		return fmt.Errorf("certificate asset write is incomplete")
	}
	stage.Metadata = value
	err := s.storeBundle(ctx, base, stage)
	stage.Metadata = nil // failed storeTx rolls back only the two successful writes
	if err != nil {
		return err
	}
	delete(s.pending, base)
	return nil
}

// storeBundle is called with s.mu held so metadata refresh and replacement
// commits cannot overwrite each other's complete bundle.
func (s *assetStorage) storeBundle(ctx context.Context, base string, bundle *certificateBundle) error {
	data, err := json.Marshal(bundle)
	if err == nil {
		err = s.Storage.Store(ctx, base+bundleSuffix, data)
	}
	if err != nil {
		s.event("storage_failed")
	}
	return err
}

func metadataCertificateURL(metadata []byte) string {
	var resource struct {
		IssuerData struct {
			URL string `json:"url"`
		} `json:"issuer_data"`
	}
	if json.Unmarshal(metadata, &resource) != nil {
		return ""
	}
	return resource.IssuerData.URL
}

func (s *assetStorage) Load(ctx context.Context, key string) ([]byte, error) {
	base, ext, asset := certificateAsset(key)
	if !asset {
		return s.Storage.Load(ctx, key)
	}
	bundle, err := s.loadBundle(ctx, base)
	if err != nil {
		return nil, err
	}
	switch ext {
	case ".key":
		return bundle.Key, nil
	case ".crt":
		return bundle.Certificate, nil
	default:
		return bundle.Metadata, nil
	}
}

func (s *assetStorage) loadBundle(ctx context.Context, base string) (*certificateBundle, error) {
	data, err := s.Storage.Load(ctx, base+bundleSuffix)
	if err != nil {
		return nil, err
	}
	var bundle certificateBundle
	if err := json.Unmarshal(data, &bundle); err != nil || bundle.Version != 1 || len(bundle.Key) == 0 || len(bundle.Certificate) == 0 || len(bundle.Metadata) == 0 {
		return nil, fmt.Errorf("invalid committed certificate bundle")
	}
	return &bundle, nil
}

func (s *assetStorage) Delete(ctx context.Context, key string) error {
	base, ext, asset := certificateAsset(key)
	if !asset {
		return s.Storage.Delete(ctx, key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pending := s.pending[base]; pending != nil {
		// Rollback of an uncommitted renewal must not delete the last good bundle.
		if ext == ".key" {
			pending.Key = nil
		}
		if ext == ".crt" {
			pending.Certificate = nil
		}
		if len(pending.Key) == 0 && len(pending.Certificate) == 0 {
			delete(s.pending, base)
		}
		return nil
	}
	return s.Storage.Delete(ctx, base+bundleSuffix)
}

func (s *assetStorage) Exists(ctx context.Context, key string) bool {
	if _, _, asset := certificateAsset(key); !asset {
		return s.Storage.Exists(ctx, key)
	}
	_, err := s.Load(ctx, key)
	return err == nil
}

func (s *assetStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	base, _, asset := certificateAsset(key)
	if !asset {
		return s.Storage.Stat(ctx, key)
	}
	value, err := s.Load(ctx, key)
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	info, err := s.Storage.Stat(ctx, base+bundleSuffix)
	info.Key, info.Size, info.IsTerminal = key, int64(len(value)), true
	return info, err
}

func (s *assetStorage) List(ctx context.Context, prefix string, recursive bool) ([]string, error) {
	if _, _, asset := certificateAsset(prefix); asset {
		if _, err := s.Load(ctx, prefix); err != nil {
			return nil, err
		}
		return []string{prefix}, nil
	}
	keys, err := s.Storage.List(ctx, prefix, recursive)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, key := range keys {
		if strings.HasSuffix(key, bundleSuffix) {
			base := strings.TrimSuffix(key, bundleSuffix)
			out = append(out, base+".key", base+".crt", base+".json")
		} else {
			out = append(out, key)
		}
	}
	return out, nil
}
