package websocket

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
)

func TestServerCloseDuringUpgrade(t *testing.T) {
	for _, tls := range []bool{false, true} {
		for _, authenticated := range []bool{false, true} {
			name := "ws/unauthenticated"
			if tls {
				name = "wss/unauthenticated"
			}
			if authenticated {
				name = strings.ReplaceAll(name, "unauthenticated", "authenticated")
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				written, release := make(chan struct{}), make(chan struct{})
				resume := sync.OnceFunc(func() { close(release) })
				cfg := Config{
					Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
					Delivery:     proxyruntime.NewDelivery(),
					LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
				}
				if authenticated {
					cfg.HandshakeHandler = proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
						return proxy.HandshakeResult{Handled: true, Identity: proxy.Identity{UID: 1001}}, nil
					})
				}
				handler := NewServer(cfg)
				transport := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler.ServeHTTP(&upgradeBarrierWriter{ResponseWriter: w, written: written, release: release}, r)
				}))
				var conn *coderws.Conn
				t.Cleanup(func() {
					resume()
					if conn != nil {
						_ = conn.CloseNow()
					}
					_ = handler.Close(context.Background())
					transport.Close()
				})
				if tls {
					transport.StartTLS()
				} else {
					transport.Start()
				}
				var err error
				conn, _, err = coderws.Dial(ctx, "ws"+strings.TrimPrefix(transport.URL, "http"), &coderws.DialOptions{HTTPClient: transport.Client()})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-written:
				case <-ctx.Done():
					t.Fatal("upgrade response was not sent")
				}
				// The client received 101 while Accept is still blocked in Hijack,
				// so the transport has not reached Delivery.Add yet.
				if err := handler.Close(ctx); err != nil {
					t.Fatal(err)
				}
				closed := conn.CloseRead(context.Background())
				resume()
				select {
				case <-closed.Done():
				case <-ctx.Done():
					t.Fatal("upgrade escaped shutdown after receiving 101")
				}
				if err := handler.Close(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// upgradeBarrierWriter pauses the successful HTTP upgrade before Accept returns.
type upgradeBarrierWriter struct {
	http.ResponseWriter
	written chan struct{}
	release <-chan struct{}
}

func (w *upgradeBarrierWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	close(w.written)
	<-w.release
	return conn, rw, nil
}
