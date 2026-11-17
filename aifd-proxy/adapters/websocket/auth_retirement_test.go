package websocket

import (
	"context"
	coderws "github.com/coder/websocket"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type reviewCloseBarrier struct {
	proxy.Connection
	entered chan struct{}
	release <-chan struct{}
}

func (c *reviewCloseBarrier) Close(ctx context.Context) error {
	close(c.entered)
	<-c.release
	return c.Connection.Close(ctx)
}
func TestInvalidConnectionCannotReenterPublicHandler(t *testing.T) {
	public := make(chan struct{}, 1)
	capture := make(chan *Connection, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	cfg := Config{AuthCheckMode: proxy.AuthCheckExpiryOnly, Gateway: proxyruntime.NewGateway(nil, nil), PublicTopics: []string{"McePublic"}, PublicHandler: proxy.PublicHandlerFunc(func(context.Context, proxy.PublicRequest) (proxy.PublicResult, error) {
		public <- struct{}{}
		return proxy.PublicResult{}, nil
	})}
	s := NewServer(cfg)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn := newConnection(r.Context(), "review-connection", ws)
		if err = s.cfg.Delivery.Add(conn); err != nil {
			t.Error(err)
			return
		}
		defer s.cleanup(r.Context(), conn.ID())
		if err = s.activateConnection(r.Context(), conn, testAuthIdentity(time.Now().Add(time.Hour))); err != nil {
			t.Error(err)
			return
		}
		capture <- conn
		s.serveConn(r.Context(), conn, r)
	}))
	defer server.Close()
	defer s.Close(context.Background())
	defer unblock()
	ws := dial(t, server)
	defer ws.CloseNow()
	conn := <-capture
	barrier := &reviewCloseBarrier{Connection: conn, entered: make(chan struct{}), release: release}
	s.cfg.Delivery.Remove(conn.ID())
	if err := s.cfg.Delivery.Add(barrier); err != nil {
		t.Fatal(err)
	}
	conn.auth.Load().fail()
	select {
	case <-barrier.entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not reach close")
	}
	if _, ok := s.cfg.Gateway.Session(conn.ID()); ok {
		t.Fatal("gateway session not removed")
	}
	writePacket(t, ws, "McePublic", "", 0, nil)
	select {
	case <-public:
		t.Fatal("invalid connection executed public handler")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
}
