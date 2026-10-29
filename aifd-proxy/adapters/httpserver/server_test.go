package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testDomain = "api.example.test"

func TestTLSConfiguration(t *testing.T) {
	valid := TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: "certs", AgreeTerms: true}
	for _, tc := range []struct {
		name string
		cfg  TLSConfig
		ok   bool
	}{
		{"default", TLSConfig{}, true},
		{"manual", TLSConfig{Mode: ModeManual, Domains: []string{testDomain}, CertFile: "cert", KeyFile: "key"}, true},
		{"acme", valid, true},
		{"unknown", TLSConfig{Mode: "automatic"}, false},
		{"plain credentials", TLSConfig{CertFile: "secret"}, false},
		{"manual missing key", TLSConfig{Mode: ModeManual, Domains: []string{testDomain}, CertFile: "cert"}, false},
		{"missing consent", TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: "certs"}, false},
		{"unknown CA", TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: "certs", AgreeTerms: true, ACMEEnvironment: "other"}, false},
		{"wildcard", TLSConfig{Mode: ModeACME, Domains: []string{"*.example.test"}, CacheDir: "certs", AgreeTerms: true}, false},
		{"IP", TLSConfig{Mode: ModeACME, Domains: []string{"127.0.0.1"}, CacheDir: "certs", AgreeTerms: true}, false},
		{"URL", TLSConfig{Mode: ModeACME, Domains: []string{"https://example.test"}, CacheDir: "certs", AgreeTerms: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, expected valid=%v", err, tc.ok)
			}
		})
	}
	valid.Domains = []string{" API.EXAMPLE.TEST. ", testDomain}
	got, err := normalizeConfig(valid)
	if err != nil || len(got.Domains) != 1 || got.Domains[0] != testDomain || got.ACMEEnvironment != "production" {
		t.Fatalf("normalized config = %+v, %v", got, err)
	}
	got.Domains[0] = "changed.test"
	if valid.Domains[0] != " API.EXAMPLE.TEST. " {
		t.Fatal("configuration aliases caller's domain slice")
	}
	if keys := TLSConfigEnvKeys("http.tls"); len(keys) != 7 || keys[0] != "http.tls.mode" {
		t.Fatalf("environment keys = %v", keys)
	}
}

func TestPlainAndManualHTTPS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "manual"}[secure], func(t *testing.T) {
			cfg, roots := manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			if !secure {
				cfg.TLS = TLSConfig{}
			}
			cfg.HTTPServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
			s, err := Start(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeTestServer(t, s) })
			if !s.Ready() {
				t.Fatalf("not ready: %+v", s.Status())
			}
			client := localTestClient(s.Addr(), roots)
			scheme := "https"
			if !secure {
				scheme = "http"
			}
			rsp, err := client.Get(scheme + "://" + testDomain + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer rsp.Body.Close()
			body, _ := io.ReadAll(rsp.Body)
			if rsp.StatusCode != 200 || string(body) != "ok" || secure && (rsp.TLS == nil || rsp.TLS.Version < tls.VersionTLS12) {
				t.Fatalf("response = %d %q, TLS=%+v", rsp.StatusCode, body, rsp.TLS)
			}
			client.CloseIdleConnections()
		})
	}
}

func TestManualTLSRejectsInvalidPeers(t *testing.T) {
	cfg, roots := manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	for _, tc := range []struct {
		name, serverName string
		roots            *x509.CertPool
		maxVersion       uint16
	}{
		{"untrusted", testDomain, x509.NewCertPool(), tls.VersionTLS13},
		{"unknown SNI", "attacker.example.test", roots, tls.VersionTLS13},
		{"old protocol", testDomain, roots, tls.VersionTLS11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", s.Addr(), &tls.Config{
				RootCAs: tc.roots, ServerName: tc.serverName, MinVersion: tls.VersionTLS10, MaxVersion: tc.maxVersion,
			})
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatal("invalid TLS peer was accepted")
			}
		})
	}
	client := localTestClient(s.Addr(), roots)
	req, _ := http.NewRequest(http.MethodGet, "https://"+testDomain+"/", nil)
	req.Host = "attacker.example.test"
	rsp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	client.CloseIdleConnections()
	if rsp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("unknown Host returned %d", rsp.StatusCode)
	}
}

func TestManualCertificateMustBeValidAndCoverDomains(t *testing.T) {
	for _, expired := range []bool{false, true} {
		cfg, _ := manualTestConfig(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		if expired {
			cfg, _ = manualTestConfig(t, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
		} else {
			cfg.TLS.Domains = []string{"other.example.test"}
		}
		if s, err := Start(context.Background(), cfg); err == nil {
			closeTestServer(t, s)
			t.Fatal("invalid manual certificate accepted")
		}
	}
}

func TestWSSAndConcurrentShutdown(t *testing.T) {
	cfg, roots := manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	var connMu sync.Mutex
	var peers []*websocket.Conn
	cfg.HTTPServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		peers = append(peers, conn)
		connMu.Unlock()
		defer conn.CloseNow()
		for {
			kind, msg, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), kind, msg); err != nil {
				return
			}
		}
	})
	closed := make(chan struct{})
	cfg.CloseWebSockets = func(context.Context) error {
		connMu.Lock()
		defer connMu.Unlock()
		for _, conn := range peers {
			_ = conn.CloseNow()
		}
		close(closed)
		return nil
	}
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := localTestClient(s.Addr(), roots)
	defer client.CloseIdleConnections()
	conn, _, err := websocket.Dial(ctx, "wss://"+testDomain+"/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("protobuf-packet")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.Read(ctx)
	if err != nil || string(msg) != "protobuf-packet" {
		t.Fatalf("WSS response %q: %v", msg, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if err := s.Close(ctx); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
	wg.Wait()
	select {
	case <-closed:
	default:
		t.Fatal("WebSocket cleanup did not run")
	}
	if s.Status().State != "stopped" || s.Ready() {
		t.Fatalf("shutdown status %+v", s.Status())
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("hijacked WebSocket remained open after shutdown")
	}
}

func TestExpiredCertificateDoesNotAdmitResumedHandshake(t *testing.T) {
	cfg, roots := manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(2*time.Second))
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	client := localTestClient(s.Addr(), roots)
	defer client.CloseIdleConnections()
	if rsp, err := client.Get("https://" + testDomain); err != nil {
		t.Fatal(err)
	} else {
		rsp.Body.Close()
	}
	until := s.Status().Certificates[0].NotAfter
	time.Sleep(time.Until(until) + 20*time.Millisecond)
	if s.Ready() || s.Status().State != "expired" {
		t.Fatalf("expired certificate still ready: %+v", s.Status())
	}
	client.CloseIdleConnections()
	if rsp, err := client.Get("https://" + testDomain); err == nil {
		rsp.Body.Close()
		t.Fatal("expired certificate accepted a new/resumed connection")
	}
}

func TestStartAndShutdownFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := Config{HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}}
	if _, err := Start(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Start: %v", err)
	}
	if _, err := Start(context.Background(), Config{}); err == nil {
		t.Fatal("missing HTTP server accepted")
	}
	sentinel := errors.New("application WebSocket close failed")
	cfg.CloseWebSockets = func(context.Context) error { return sentinel }
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("close error not preserved: %v", err)
	}
}

func manualTestConfig(t *testing.T, from, until time.Time) (Config, *x509.CertPool) {
	t.Helper()
	certPEM, keyPEM, root := makeTestCertificate(t, []string{testDomain}, from, until)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{
		HTTPServer:      &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		TLS:             TLSConfig{Mode: ModeManual, Domains: []string{testDomain}, CertFile: certFile, KeyFile: keyFile},
		ShutdownTimeout: 3 * time.Second,
	}, root
}

func makeTestCertificate(t *testing.T, names []string, from, until time.Time) ([]byte, []byte, *x509.CertPool) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AIFD test CA"},
		NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, rootKey.Public(), rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, _ = x509.ParseCertificate(rootDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	leaf := &x509.Certificate{SerialNumber: serial, DNSNames: names, NotBefore: from, NotAfter: until,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	// The test ACME endpoint is reached by loopback IP, not by a public DNS name.
	leaf.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, key.Public(), rootKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})...)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), pool
}

func localTestClient(addr string, roots *x509.CertPool) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: testDomain, ClientSessionCache: tls.NewLRUClientSessionCache(8)},
	}}
}

func closeTestServer(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Errorf("server cleanup: %v", err)
	}
	select {
	case <-s.Done():
	default:
		t.Error("Close returned before Done")
	}
}

func TestStatusIsDetached(t *testing.T) {
	cfg, _ := manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	status := s.Status()
	status.Certificates[0].Domain = "changed.example.test"
	if strings.Contains(s.Status().Certificates[0].Domain, "changed") {
		t.Fatal("status aliases internal state")
	}
}

func TestACMEPendingIsFailClosedAndCancellationReleasesListener(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "https://" + closed.Addr().String() + "/dir"
	_ = closed.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := Config{HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		TLS:             TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: t.TempDir(), AgreeTerms: true},
		ShutdownTimeout: time.Second}
	s, err := start(ctx, cfg, func(ctx context.Context, cfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		return newACMEProvider(ctx, cfg, addr, event, acmeOptions{directoryURL: url})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	if s.Ready() || s.Status().State != "pending" {
		t.Fatalf("cold start without CA access reported ready: %+v", s.Status())
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", s.Addr(), &tls.Config{ServerName: testDomain})
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("pending certificate admitted a business TLS connection")
	}
	addr := s.Addr()
	cancel()
	closeTestServer(t, s)
	rebound, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listener was not released: %v", err)
	}
	rebound.Close()
}

func TestShutdownDeadlineForceClosesActiveHTTP(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	s, err := Start(context.Background(), Config{
		HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(stopped)
		})}, ShutdownTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: 3 * time.Second}
		if rsp, err := client.Get("http://" + s.Addr()); err == nil {
			rsp.Body.Close()
		}
		client.CloseIdleConnections()
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown timeout not reported: %v", err)
	}
	for _, done := range []<-chan struct{}{stopped, requestDone, s.Done()} {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("shutdown left an active request behind")
		}
	}
}

func TestCloseCancelsAnInflightACMERequest(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var once, cancelOnce sync.Once
	ca := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
		cancelOnce.Do(func() { close(cancelled) })
	}))
	defer ca.Close()
	cfg := Config{HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		TLS:             TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: t.TempDir(), AgreeTerms: true},
		ShutdownTimeout: time.Second}
	s, err := start(context.Background(), cfg, func(ctx context.Context, cfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		return newACMEProvider(ctx, cfg, addr, event, acmeOptions{directoryURL: ca.URL + "/dir",
			trustedRoots: ca.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("ACME request did not start")
	}
	closeTestServer(t, s)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight certificate request continued after Close")
	}
}
