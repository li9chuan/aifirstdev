package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	log "github.com/li9chuan/aifirstdev/aifd-observe/logging"
	"go.uber.org/zap"
)

// Test seams are instance-local. Production configuration intentionally exposes
// no arbitrary CA URL, untrusted root pool, storage implementation or scan timer.
type acmeOptions struct {
	directoryURL  string
	trustedRoots  *x509.CertPool
	renewInterval time.Duration
	storage       certmagic.Storage
}

type acmeProvider struct {
	manager *certmagic.Config
	cache   *certmagic.Cache
	issuer  *managedACMEIssuer
	event   func(string)
	storage *managedStorage
	ctx     context.Context
	cancel  context.CancelFunc
	domains []string
	once    sync.Once
	done    chan struct{}
}

func newACMEProvider(ctx context.Context, cfg TLSConfig, _ net.Addr, event func(string), opts acmeOptions) (*acmeProvider, error) {
	dir, err := filepath.Abs(filepath.Join(cfg.CacheDir, cfg.ACMEEnvironment))
	if err != nil {
		return nil, fmt.Errorf("http acme: invalid cache directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("http acme: create private storage: %w", err)
	}
	if err := secureDirectory(dir); err != nil {
		return nil, fmt.Errorf("http acme: private storage: %w", err)
	}
	store := opts.storage
	if store == nil {
		store = &certmagic.FileStorage{Path: dir}
	}
	runCtx, cancel := context.WithCancel(ctx)
	p := &acmeProvider{domains: append([]string(nil), cfg.Domains...), event: event, ctx: runCtx, cancel: cancel, done: make(chan struct{})}
	p.storage = &managedStorage{Storage: store}
	// The library logger is deliberately silent: raw library events may contain
	// challenge URLs and private asset paths. Only whitelisted outcomes are logged.
	p.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert:   func(certmagic.Certificate) (*certmagic.Config, error) { return p.manager, nil },
		RenewCheckInterval: opts.renewInterval,
		Logger:             zap.NewNop(),
	})
	p.manager = certmagic.New(p.cache, certmagic.Config{
		Storage: newAssetStorage(p.storage, event),
		Logger:  zap.NewNop(),
		OnEvent: func(_ context.Context, name string, data map[string]any) error {
			switch name {
			case "cert_failed":
				code := "issuance_failed"
				if renewal, _ := data["renewal"].(bool); renewal {
					code = "renewal_failed"
				}
				event(code)
			case "cert_obtained":
				event("")
				log.Info("http certificate obtained and stored")
			}
			return nil
		},
	})
	directory := certmagic.LetsEncryptProductionCA
	if cfg.ACMEEnvironment == "staging" {
		directory = certmagic.LetsEncryptStagingCA
	}
	if opts.directoryURL != "" {
		directory = opts.directoryURL
	}
	p.issuer, err = newManagedACMEIssuer(ctx, p, directory, opts.trustedRoots)
	if err != nil {
		cancel()
		p.cache.Stop()
		return nil, err
	}
	p.manager.Issuers = []certmagic.Issuer{p.issuer}
	return p, nil
}

func (p *acmeProvider) TLSConfig() *tls.Config {
	cfg := p.manager.TLSConfig()
	cfg.NextProtos = append(cfg.NextProtos, "acme-tls/1")
	getCertificate := cfg.GetCertificate
	cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if cert := p.issuer.challengeCertificate(hello); cert != nil {
			return cert, nil
		}
		return getCertificate(hello)
	}
	return cfg
}

func (p *acmeProvider) Certificates(domain string) []*x509.Certificate {
	var out []*x509.Certificate
	for _, cert := range p.cache.AllMatchingCertificates(domain) {
		out = append(out, cert.Leaf)
	}
	return out
}

func (p *acmeProvider) Start(ctx context.Context) error {
	// Missing is a normal cold start. A corrupt or unreadable existing asset is
	// not silently discarded or replaced by a new identity/order.
	for _, domain := range p.domains {
		if _, err := p.manager.CacheManagedCertificate(ctx, domain); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot restore existing certificate for %s", domain)
		}
	}
	return p.manager.ManageAsync(p.ctx, p.domains)
}

func (p *acmeProvider) Close(ctx context.Context) error {
	p.once.Do(func() {
		p.cancel()
		p.storage.stopNewJobs()
		go func() {
			p.cache.Stop()
			p.storage.jobs.Wait()
			p.issuer.client.HTTPClient.CloseIdleConnections()
			close(p.done)
		}()
	})
	select {
	case <-p.done:
		return p.storage.closeError()
	case <-ctx.Done():
		return fmt.Errorf("http acme maintenance shutdown: %w", ctx.Err())
	}
}

func (s *Server) certificateEvent(code string) {
	s.mu.Lock()
	previous := s.lastCode
	s.lastCode = code
	s.mu.Unlock()
	if code != "" && code != previous {
		log.Warn("http certificate management degraded", log.String("addr", s.Addr()), log.String("error_code", code))
	} else if code == "" && previous != "" {
		log.Info("http certificate management recovered")
	}
}
