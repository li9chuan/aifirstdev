package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/li9chuan/aifirstdev/aifd-observe/logging"
)

const defaultShutdownTimeout = 30 * time.Second

// Config transfers ownership of an unused HTTP server to this adapter.
// CloseWebSockets must close hijacked connections when the handler owns any;
// http.Server.Shutdown does not close WebSockets. It must honor its context.
type Config struct {
	HTTPServer      *http.Server
	TLS             TLSConfig
	CloseWebSockets func(context.Context) error
	ShutdownTimeout time.Duration
}

// CertificateStatus describes public certificate metadata, never key material.
type CertificateStatus struct {
	Domain       string
	NotBefore    time.Time
	NotAfter     time.Time
	SerialNumber string
}

// Status is a point-in-time view. Degraded remains Ready while all certs are valid.
type Status struct {
	Mode          Mode
	State         string
	Ready         bool
	LastErrorCode string
	Certificates  []CertificateStatus
}

// Server owns one listener and one certificate manager. Start, then Close/Done;
// instances are not reusable. Persistent certificate storage is never deleted.
type Server struct {
	http     *http.Server
	listener net.Listener
	cfg      Config
	tlsCfg   TLSConfig
	provider certificateProvider
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	stopping bool
	stopped  bool
	lastCode string
	err      error
}

type certificateProvider interface {
	TLSConfig() *tls.Config
	Certificates(string) []*x509.Certificate
	Start(context.Context) error
	Close(context.Context) error
}

type providerFactory func(context.Context, TLSConfig, net.Addr, func(string)) (certificateProvider, error)

// Start binds the configured address and starts serving. ACME may initially be
// pending; use Ready/Status in the application's existing readiness probe.
// Construction elsewhere and importing the package perform no ACME operations.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	return start(ctx, cfg, newCertificateProvider)
}

func start(ctx context.Context, cfg Config, factory providerFactory) (*Server, error) {
	if ctx == nil {
		return nil, fmt.Errorf("http server: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.HTTPServer == nil || cfg.HTTPServer.Handler == nil || strings.TrimSpace(cfg.HTTPServer.Addr) == "" {
		return nil, fmt.Errorf("http server: unused server with explicit addr and handler is required")
	}
	if cfg.HTTPServer.TLSConfig != nil || cfg.HTTPServer.TLSNextProto != nil {
		return nil, fmt.Errorf("http server: TLS configuration is owned by the adapter")
	}
	tlsCfg, err := normalizeConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	if cfg.ShutdownTimeout < 0 {
		return nil, fmt.Errorf("http server: shutdown_timeout must not be negative")
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	ln, err := net.Listen("tcp", cfg.HTTPServer.Addr)
	if err != nil {
		return nil, fmt.Errorf("http server listen: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Server{http: cfg.HTTPServer, listener: ln, cfg: cfg, tlsCfg: tlsCfg, cancel: cancel, done: make(chan struct{})}
	if tlsCfg.Mode != ModePlain {
		s.provider, err = factory(runCtx, tlsCfg, ln.Addr(), s.certificateEvent)
		if err != nil {
			cancel()
			_ = ln.Close()
			return nil, err
		}
		s.http.TLSConfig = s.transportTLSConfig()
	}
	if s.http.ReadHeaderTimeout == 0 {
		s.http.ReadHeaderTimeout = 5 * time.Second
	}
	if s.http.MaxHeaderBytes == 0 {
		s.http.MaxHeaderBytes = 1 << 20
	}
	original := s.http.Handler
	s.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.tlsCfg.Mode != ModePlain && !s.allowedHost(r.Host) {
			http.Error(w, "unrecognized host", http.StatusMisdirectedRequest)
			return
		}
		if !s.Ready() {
			http.Error(w, "transport not ready", http.StatusServiceUnavailable)
			return
		}
		original.ServeHTTP(w, r)
	})
	serveResult := make(chan error, 1)
	go func() {
		if s.provider == nil {
			serveResult <- s.http.Serve(ln)
		} else {
			serveResult <- s.http.ServeTLS(ln, "", "")
		}
	}()
	var managementErr error
	if s.provider != nil {
		managementErr = s.provider.Start(runCtx)
	}
	if managementErr == nil {
		managementErr = ctx.Err()
	}
	go s.run(runCtx, serveResult)
	if managementErr != nil {
		_ = s.Close(context.Background())
		return nil, fmt.Errorf("http certificate management: %w", managementErr)
	}
	log.Info("http transport started", log.String("mode", string(tlsCfg.Mode)), log.String("addr", s.Addr()))
	return s, nil
}

// Addr returns the bound address, including the allocated port when using :0.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Done closes after listener, WebSockets, HTTP and certificate maintenance stop.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err returns a serving/shutdown failure after Done; normal cancellation is nil.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Ready reports transport readiness; the application combines it with its own gate.
func (s *Server) Ready() bool { return s.Status().Ready }

// Status reads current certificate metadata without initiating network operations.
func (s *Server) Status() Status {
	s.mu.Lock()
	stopping, stopped, code := s.stopping, s.stopped, s.lastCode
	s.mu.Unlock()
	out := Status{Mode: s.tlsCfg.Mode, State: "ready", Ready: true, LastErrorCode: code}
	if stopping || stopped {
		out.State, out.Ready = "stopping", false
		if stopped {
			out.State = "stopped"
		}
		return out
	}
	now := time.Now()
	for _, domain := range s.tlsCfg.Domains {
		cert := selectCertificate(s.provider.Certificates(domain), domain, now)
		info := CertificateStatus{Domain: domain}
		if cert != nil {
			info.NotBefore, info.NotAfter = cert.NotBefore, cert.NotAfter
			info.SerialNumber = cert.SerialNumber.String()
		}
		out.Certificates = append(out.Certificates, info)
		if cert == nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			out.Ready = false
			if out.State != "expired" {
				out.State = "pending"
			}
			if cert != nil && !now.Before(cert.NotAfter) {
				out.State = "expired"
			}
		}
	}
	if out.Ready && code != "" {
		out.State = "degraded"
	}
	return out
}

// Close is idempotent and waits for cleanup, bounded by the caller's context.
func (s *Server) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("http server close: context is required")
	}
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.cancel()
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) transportTLSConfig() *tls.Config {
	cfg := s.provider.TLSConfig().Clone()
	cfg.MinVersion = tls.VersionTLS12
	cfg.NextProtos = append(cfg.NextProtos, "h2", "http/1.1")
	// This also runs on resumed sessions, where GetCertificate may not run.
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		name, err := normalizeDomain(hello.ServerName)
		if err != nil || !s.allowedDomain(name) {
			return nil, fmt.Errorf("http tls: unrecognized server name")
		}
		if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == "acme-tls/1" && s.tlsCfg.Mode == ModeACME {
			return nil, nil
		}
		s.mu.Lock()
		stopping := s.stopping
		s.mu.Unlock()
		now := time.Now()
		cert := selectCertificate(s.provider.Certificates(name), name, now)
		if stopping || cert == nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, fmt.Errorf("http tls: certificate unavailable")
		}
		return nil, nil
	}
	return cfg
}

func (s *Server) allowedDomain(name string) bool {
	for _, domain := range s.tlsCfg.Domains {
		if name == domain {
			return true
		}
	}
	return false
}

func (s *Server) allowedHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	name, err := normalizeDomain(host)
	return err == nil && s.allowedDomain(name)
}

func (s *Server) run(ctx context.Context, serveResult <-chan error) {
	var serveErr error
	serveFinished := false
	select {
	case <-ctx.Done():
	case serveErr = <-serveResult:
		serveFinished = true
	}
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.cancel()
	_ = s.listener.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	var closeErr error
	if s.cfg.CloseWebSockets != nil {
		closeErr = s.cfg.CloseWebSockets(shutdownCtx)
	}
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		closeErr = errors.Join(closeErr, err, s.http.Close())
	}
	if !serveFinished {
		select {
		case serveErr = <-serveResult:
		case <-shutdownCtx.Done():
			closeErr = errors.Join(closeErr, shutdownCtx.Err())
		}
	}
	if s.provider != nil {
		closeErr = errors.Join(closeErr, s.provider.Close(shutdownCtx))
	}
	if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	}
	s.mu.Lock()
	s.err, s.stopped = errors.Join(serveErr, closeErr), true
	s.mu.Unlock()
	close(s.done)
}

func selectCertificate(certs []*x509.Certificate, domain string, now time.Time) *x509.Certificate {
	var selected *x509.Certificate
	for _, cert := range certs {
		if cert == nil || cert.VerifyHostname(domain) != nil {
			continue
		}
		if selected == nil || cert.NotAfter.After(selected.NotAfter) {
			selected = cert
		}
		if !now.Before(cert.NotBefore) && now.Before(cert.NotAfter) {
			return cert
		}
	}
	return selected
}
