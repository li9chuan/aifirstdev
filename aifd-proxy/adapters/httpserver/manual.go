package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

func newCertificateProvider(ctx context.Context, cfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
	if cfg.Mode == ModeACME {
		return newACMEProvider(ctx, cfg, addr, event, acmeOptions{})
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("http tls: cannot load certificate/key pair: %w", err)
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("http tls: cannot parse certificate: %w", err)
	}
	now := time.Now()
	if now.Before(cert.Leaf.NotBefore) || !now.Before(cert.Leaf.NotAfter) {
		return nil, fmt.Errorf("http tls: manual certificate is not currently valid")
	}
	for _, domain := range cfg.Domains {
		if cert.Leaf.VerifyHostname(domain) != nil {
			return nil, fmt.Errorf("http tls: manual certificate does not cover %s", domain)
		}
	}
	return &manualProvider{cert: cert}, nil
}

type manualProvider struct{ cert tls.Certificate }

func (p *manualProvider) TLSConfig() *tls.Config {
	return &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &p.cert, nil }}
}
func (p *manualProvider) Certificates(string) []*x509.Certificate {
	return []*x509.Certificate{p.cert.Leaf}
}
func (p *manualProvider) Start(context.Context) error { return nil }
func (p *manualProvider) Close(context.Context) error { return nil }
