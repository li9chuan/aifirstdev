package httpserver

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

func TestACMEProviderRetainsCooldownAcrossRestart(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int64
			ca := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Retry-After", "600")
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"type":"urn:ietf:params:acme:error:rateLimited","status":%d}`, status)
			}))
			defer ca.Close()
			roots := x509.NewCertPool()
			roots.AddCert(ca.Certificate())
			cache := t.TempDir()
			startServer := func() *Server {
				cfg := Config{
					HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
					TLS: TLSConfig{Mode: ModeACME, Domains: []string{"api.example.test"},
						CacheDir: cache, ACMEEnvironment: "staging", AgreeTerms: true},
				}
				s, err := start(context.Background(), cfg, func(ctx context.Context, tlsCfg TLSConfig, addr net.Addr, event func(string)) (certificateProvider, error) {
					return newACMEProvider(ctx, tlsCfg, addr, event, acmeOptions{directoryURL: ca.URL + "/dir", trustedRoots: roots})
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { closeTestServer(t, s) })
				return s
			}
			first := startServer()
			disk := &certmagic.FileStorage{Path: filepath.Join(cache, "staging")}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := disk.Load(context.Background(), retryStateKey); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("CA response did not persist cooldown")
				}
				time.Sleep(5 * time.Millisecond)
			}
			closeTestServer(t, first)
			before := requests.Load()
			if before != 1 {
				t.Fatalf("library retried before cooldown: %d requests", before)
			}
			second := startServer()
			// Let the new asynchronous manager attempt startup. It must wait on
			// restored state rather than sending a directory/account request.
			time.Sleep(120 * time.Millisecond)
			if requests.Load() != before || second.Ready() || second.Status().LastErrorCode != "ca_cooldown" {
				t.Fatal("restart bypassed persisted cooldown")
			}
			closedAt := time.Now()
			closeTestServer(t, second)
			if time.Since(closedAt) > time.Second {
				t.Fatal("shutdown waited for CA cooldown to expire")
			}
		})
	}
}
