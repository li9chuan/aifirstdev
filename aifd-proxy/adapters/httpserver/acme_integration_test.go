//go:build integration

package httpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/coder/websocket"
	"github.com/miekg/dns"
)

func TestACMEIssuanceAutomaticRenewalAndOfflineRecovery(t *testing.T) {
	if os.Getenv("AIFD_PEBBLE_BIN") == "" {
		t.Skip("set AIFD_PEBBLE_BIN to run real local ACME integration")
	}
	var fixture *pebbleFixture
	var provider *acmeProvider
	var alpnValidations atomic.Int64
	var peerMu sync.Mutex
	var peers []*websocket.Conn
	cfg := Config{
		HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/ws" {
				_, _ = io.WriteString(w, "ready")
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			peerMu.Lock()
			peers = append(peers, conn)
			peerMu.Unlock()
			defer conn.CloseNow()
			for {
				kind, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				if conn.Write(r.Context(), kind, data) != nil {
					return
				}
			}
		})},
		TLS: TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: t.TempDir(), AgreeTerms: true, ACMEEnvironment: "staging"},
		CloseWebSockets: func(context.Context) error {
			peerMu.Lock()
			defer peerMu.Unlock()
			for _, peer := range peers {
				_ = peer.CloseNow()
			}
			return nil
		},
		ShutdownTimeout: 5 * time.Second,
	}
	s, err := start(context.Background(), cfg, func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		fixture = startPebble(t, addr.(*net.TCPAddr).Port, 12)
		created, createErr := newACMEProvider(ctx, tlsCfg, addr, event, fixture.options())
		provider = created
		if createErr != nil {
			return nil, createErr
		}
		return &recordingProvider{certificateProvider: created, validations: &alpnValidations}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	waitUntil(t, 20*time.Second, "initial certificate", s.Ready)
	first := s.Status().Certificates[0].SerialNumber
	client := localTestClient(s.Addr(), fixture.signingRoots)
	defer client.CloseIdleConnections()
	if got := wireSerial(t, client); got != first {
		t.Fatalf("initial wire serial %s != status %s", got, first)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "wss://"+testDomain+"/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	waitUntil(t, 20*time.Second, "automatic renewal", func() bool {
		status := s.Status()
		return status.Ready && status.Certificates[0].SerialNumber != first
	})
	second := s.Status().Certificates[0].SerialNumber
	client.CloseIdleConnections()
	if got := wireSerial(t, client); got != second {
		t.Fatalf("renewed wire serial %s != status %s", got, second)
	}
	key := certmagic.StorageKeys.SiteCert(provider.manager.Issuers[0].IssuerKey(), testDomain)
	stored, err := provider.manager.Storage.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(stored)
	if block == nil {
		t.Fatal("stored certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert.SerialNumber.String() != second {
		t.Fatalf("disk certificate did not rotate: %v", err)
	}
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("still-connected-after-renewal")); err != nil {
		t.Fatal(err)
	}
	_, message, err := ws.Read(ctx)
	if err != nil || string(message) != "still-connected-after-renewal" {
		t.Fatalf("existing WSS connection interrupted by renewal: %q %v", message, err)
	}
	if fixture.dnsQueries.Load() == 0 || alpnValidations.Load() == 0 {
		t.Fatalf("real TLS-ALPN verification was not observed:\n%s", fixture.logs.String())
	}
	closeTestServer(t, s)
	fixture.stop()
	// Recover using the same cert/account storage with the CA completely offline.
	accountLocation := provider.issuer.account.Location
	accountThumbprint, err := provider.issuer.account.Thumbprint()
	if err != nil || accountLocation == "" {
		t.Fatalf("issued certificate has no reusable account: %v", err)
	}
	cfg.HTTPServer = &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	cfg.CloseWebSockets = nil
	recovered, err := start(context.Background(), cfg, func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		return newACMEProvider(ctx, tlsCfg, addr, event, fixture.options())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, recovered) })
	restoredProvider := recovered.provider.(*acmeProvider)
	if thumbprint, err := restoredProvider.issuer.account.Thumbprint(); err != nil || thumbprint != accountThumbprint || restoredProvider.issuer.account.Location != accountLocation {
		t.Fatalf("restart did not preserve ACME account identity: %v", err)
	}
	if !recovered.Ready() || recovered.Status().Certificates[0].SerialNumber != second {
		t.Fatalf("offline cache recovery failed: %+v", recovered.Status())
	}
	recoveryClient := localTestClient(recovered.Addr(), fixture.signingRoots)
	defer recoveryClient.CloseIdleConnections()
	if got := wireSerial(t, recoveryClient); got != second {
		t.Fatalf("offline recovered wire serial %s != %s", got, second)
	}
	t.Logf("real TLS-ALPN issuance, scheduled renewal, disk/wire serial rotation, uninterrupted WSS and offline recovery passed (%s -> %s)", first, second)
}

func wireSerial(t *testing.T, client *http.Client) string {
	t.Helper()
	// A resumed TLS session deliberately retains its original peer certificate.
	// Inspect a fresh, non-resumed handshake when proving certificate rotation.
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ClientSessionCache = nil
	transport.DisableKeepAlives = true
	fresh := &http.Client{Transport: transport, Timeout: client.Timeout}
	defer transport.CloseIdleConnections()
	rsp, err := fresh.Get("https://" + testDomain + "/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rsp.Body)
	rsp.Body.Close()
	return rsp.TLS.PeerCertificates[0].SerialNumber.String()
}

func TestACMERenewalInformationRefreshSurvivesRestart(t *testing.T) {
	if os.Getenv("AIFD_PEBBLE_BIN") == "" {
		t.Skip("set AIFD_PEBBLE_BIN to run real local ACME integration")
	}
	var fixture *pebbleFixture
	var provider *acmeProvider
	cfg := Config{
		HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		TLS: TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: t.TempDir(),
			AgreeTerms: true, ACMEEnvironment: "staging"},
		ShutdownTimeout: 5 * time.Second,
	}
	factory := func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		if fixture == nil {
			fixture = startPebble(t, addr.(*net.TCPAddr).Port, 3600)
		}
		created, err := newACMEProvider(ctx, tlsCfg, addr, event, fixture.options())
		provider = created
		return created, err
	}
	s, err := start(context.Background(), cfg, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	waitUntil(t, 20*time.Second, "initial certificate", s.Ready)
	serial := s.Status().Certificates[0].SerialNumber
	closeTestServer(t, s)
	ctx := context.Background()
	key := certmagic.StorageKeys.SiteMeta(provider.manager.Issuers[0].IssuerKey(), testDomain)
	data, err := provider.manager.Storage.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var resource certmagic.CertificateResource
	if err := json.Unmarshal(data, &resource); err != nil {
		t.Fatal(err)
	}
	var issued map[string]json.RawMessage
	if err := json.Unmarshal(resource.IssuerData, &issued); err != nil {
		t.Fatal(err)
	}
	var ari map[string]json.RawMessage
	if err := json.Unmarshal(issued["renewal_info"], &ari); err != nil || ari == nil {
		t.Fatalf("Pebble did not provide initial ARI: %s, %v", resource.IssuerData, err)
	}
	// Make the stored next-poll time due, then let a fresh provider refresh ARI
	// through the real ACME endpoint and its normal maintenance loop.
	ari["_retryAfter"], _ = json.Marshal(time.Now().Add(-time.Hour))
	issued["renewal_info"], _ = json.Marshal(ari)
	resource.IssuerData, _ = json.Marshal(issued)
	data, _ = json.Marshal(resource)
	if err := provider.manager.Storage.Store(ctx, key, data); err != nil {
		t.Fatal(err)
	}
	cfg.HTTPServer = &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	recovered, err := start(ctx, cfg, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, recovered) })
	var refreshed []byte
	waitUntil(t, 10*time.Second, "persisted ARI refresh", func() bool {
		refreshed, err = provider.manager.Storage.Load(ctx, key)
		if err != nil || json.Unmarshal(refreshed, &resource) != nil {
			return false
		}
		var metadata struct {
			RenewalInfo struct {
				RetryAfter time.Time `json:"_retryAfter"`
			} `json:"renewal_info"`
		}
		return json.Unmarshal(resource.IssuerData, &metadata) == nil && metadata.RenewalInfo.RetryAfter.After(time.Now())
	})
	if status := recovered.Status(); !status.Ready || status.Certificates[0].SerialNumber != serial {
		t.Fatalf("ARI refresh changed certificate availability: %+v", status)
	}
	closeTestServer(t, recovered)
	restarted := newAssetStorage(&certmagic.FileStorage{Path: filepath.Join(cfg.TLS.CacheDir, "staging")}, func(string) {})
	if stored, err := restarted.Load(ctx, key); err != nil || !bytes.Equal(stored, refreshed) {
		t.Fatalf("refreshed ARI did not survive restart: %v", err)
	}
	t.Log("real ARI refresh persisted the CA's next-poll time without reissuing the certificate")
}

func TestACMERenewalWriteFailurePreservesOfflineRecovery(t *testing.T) {
	if os.Getenv("AIFD_PEBBLE_BIN") == "" {
		t.Skip("set AIFD_PEBBLE_BIN to run real local ACME integration")
	}
	var fixture *pebbleFixture
	var provider *acmeProvider
	cacheDir := t.TempDir()
	store := &faultStorage{Storage: &certmagic.FileStorage{Path: filepath.Join(cacheDir, "staging")}}
	cfg := Config{HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		TLS:             TLSConfig{Mode: ModeACME, Domains: []string{testDomain}, CacheDir: cacheDir, AgreeTerms: true, ACMEEnvironment: "staging"},
		ShutdownTimeout: 5 * time.Second}
	factory := func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		options := fixture.options()
		options.storage = store
		created, err := newACMEProvider(ctx, tlsCfg, addr, event, options)
		provider = created
		return created, err
	}
	s, err := start(context.Background(), cfg, func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
		fixture = startPebble(t, addr.(*net.TCPAddr).Port, 3600)
		return factory(ctx, tlsCfg, addr, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestServer(t, s) })
	waitUntil(t, 20*time.Second, "initial certificate", s.Ready)
	oldSerial := s.Status().Certificates[0].SerialNumber
	store.fail.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := provider.manager.RenewCertSync(ctx, testDomain, true); err == nil || store.failures.Load() == 0 {
		t.Fatalf("real certificate storage failure was not exercised: %v", err)
	}
	if status := s.Status(); !status.Ready || status.State != "degraded" || status.LastErrorCode != "storage_failed" {
		t.Fatalf("failed renewal lost the valid certificate: %+v", status)
	}
	client := localTestClient(s.Addr(), fixture.signingRoots)
	defer client.CloseIdleConnections()
	if serial := wireSerial(t, client); serial != oldSerial {
		t.Fatalf("uncommitted certificate became active: %s", serial)
	}
	closeTestServer(t, s)
	fixture.stop()
	cfg.HTTPServer = &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	recovered, err := start(context.Background(), cfg, factory)
	if err != nil {
		t.Fatalf("old certificate was damaged by failed renewal: %v", err)
	}
	t.Cleanup(func() { closeTestServer(t, recovered) })
	if !recovered.Ready() || recovered.Status().Certificates[0].SerialNumber != oldSerial {
		t.Fatalf("old certificate did not survive restart: %+v", recovered.Status())
	}
	key := certmagic.StorageKeys.SiteCert(provider.manager.Issuers[0].IssuerKey(), testDomain)
	closeTestServer(t, recovered)
	// A corrupt committed asset is an error, not an empty cache or a new order.
	if err := store.Storage.Store(ctx, strings.TrimSuffix(key, ".crt")+bundleSuffix, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	cfg.HTTPServer = &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	if broken, err := start(context.Background(), cfg, factory); err == nil {
		closeTestServer(t, broken)
		t.Fatal("corrupt certificate was treated as a cache miss")
	}
	t.Log("real renewal storage failure preserved the old disk/key pair and offline recovery; corrupt committed storage was rejected")
}

type faultStorage struct {
	certmagic.Storage
	fail     atomic.Bool
	failures atomic.Int64
}

func (s *faultStorage) Store(ctx context.Context, key string, value []byte) error {
	if s.fail.Load() && strings.HasSuffix(key, bundleSuffix) {
		s.failures.Add(1)
		return errors.New("synthetic certificate disk-full failure")
	}
	return s.Storage.Store(ctx, key, value)
}

type recordingProvider struct {
	certificateProvider
	validations *atomic.Int64
}

func (p *recordingProvider) TLSConfig() *tls.Config {
	cfg := p.certificateProvider.TLSConfig().Clone()
	getCertificate := cfg.GetCertificate
	cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == "acme-tls/1" {
			p.validations.Add(1)
		}
		return getCertificate(hello)
	}
	return cfg
}

type pebbleFixture struct {
	cmd          *exec.Cmd
	logs         *lockedBuffer
	apiRoots     *x509.CertPool
	signingRoots *x509.CertPool
	directoryURL string
	dnsQueries   atomic.Int64
	stopOnce     sync.Once
}

func (p *pebbleFixture) options() acmeOptions {
	return acmeOptions{directoryURL: p.directoryURL, trustedRoots: p.apiRoots, renewInterval: 200 * time.Millisecond}
}

func (p *pebbleFixture) stop() {
	p.stopOnce.Do(func() {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
}

func startPebble(t *testing.T, tlsPort, validitySeconds int) *pebbleFixture {
	t.Helper()
	bin := os.Getenv("AIFD_PEBBLE_BIN")
	if bin == "" {
		t.Skip("set AIFD_PEBBLE_BIN to a Pebble v2.10.1 executable; validation is never bypassed")
	}
	p := &pebbleFixture{logs: &lockedBuffer{}}
	dnsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dnsServer := &dns.Server{Listener: dnsListener, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		p.dnsQueries.Add(1)
		m := new(dns.Msg)
		m.SetReply(q)
		m.Authoritative = true
		for _, question := range q.Question {
			if question.Qtype == dns.TypeA {
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.IPv4(127, 0, 0, 1)})
			}
		}
		_ = w.WriteMsg(m)
	})}
	go func() { _ = dnsServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = dnsServer.Shutdown() })
	apiAddr, managementAddr := freeAddress(t), freeAddress(t)
	p.directoryURL = "https://" + apiAddr + "/dir"
	dir := t.TempDir()
	certPEM, keyPEM, roots := makeTestCertificate(t, []string{"localhost"}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	p.apiRoots = roots
	certPath, keyPath := filepath.Join(dir, "api.pem"), filepath.Join(dir, "api-key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"pebble": map[string]any{
		"listenAddress": apiAddr, "managementListenAddress": managementAddr,
		"certificate": certPath, "privateKey": keyPath,
		"httpPort": 0, "tlsPort": tlsPort, "keyAlgorithm": "ecdsa",
		"profiles":   map[string]any{"default": map[string]any{"description": "local integration", "validityPeriod": validitySeconds}},
		"retryAfter": map[string]int{"authz": 0, "order": 0},
	}})
	configPath := filepath.Join(dir, "pebble.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	p.cmd = exec.Command(bin, "-config", configPath, "-dnsserver", dnsListener.Addr().String())
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "PEBBLE_") {
			p.cmd.Env = append(p.cmd.Env, value)
		}
	}
	p.cmd.Env = append(p.cmd.Env, "PEBBLE_VA_NOSLEEP=1", "PEBBLE_VA_ALWAYS_VALID=0", "PEBBLE_WFE_NONCEREJECT=0")
	p.cmd.Stdout, p.cmd.Stderr = p.logs, p.logs
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			t.Log(p.logs.String())
		}
	})
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	t.Cleanup(client.CloseIdleConnections)
	waitUntil(t, 10*time.Second, "Pebble API", func() bool {
		rsp, err := client.Get(p.directoryURL)
		if err != nil {
			return false
		}
		rsp.Body.Close()
		return rsp.StatusCode == 200
	})
	rsp, err := client.Get("https://" + managementAddr + "/roots/0")
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	rootPEM, _ := io.ReadAll(rsp.Body)
	p.signingRoots = x509.NewCertPool()
	if !p.signingRoots.AppendCertsFromPEM(rootPEM) {
		t.Fatalf("cannot load Pebble signing root: %s", rootPEM)
	}
	return p
}

func freeAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitUntil(t *testing.T, timeout time.Duration, name string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s after %s", name, timeout)
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
