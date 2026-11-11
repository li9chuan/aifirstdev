package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseWhileHTTPListenerIsRegistered(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, cancelParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("tls=%t/parent_cancel=%t", secure, cancelParent), func(t *testing.T) {
				cfg := Config{HTTPServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}}
				if secure {
					cfg, _ = manualTestConfig(t, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
				}
				registered, releaseServe := holdHTTPServeUntilShutdown(t, cfg.HTTPServer)
				var wsCloses atomic.Int64
				cfg.CloseWebSockets = func(context.Context) error { wsCloses.Add(1); return nil }
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				s, err := Start(parent, cfg)
				if err != nil {
					releaseServe()
					t.Fatal(err)
				}
				t.Cleanup(func() { releaseServe(); closeTestServer(t, s) })
				select {
				case <-registered:
				case <-time.After(3 * time.Second):
					t.Fatal("HTTP listener was not registered")
				}
				if cancelParent {
					cancel()
				}
				ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				var callers sync.WaitGroup
				results := make(chan error, 8)
				for range 8 {
					callers.Go(func() { results <- s.Close(ctx) })
				}
				callers.Wait()
				close(results)
				for err := range results {
					if err != nil {
						t.Fatalf("normal shutdown returned an error: %v", err)
					}
				}
				select {
				case <-s.Done():
				default:
					t.Fatal("Close returned before Done")
				}
				if s.Err() != nil || s.Ready() || s.Status().State != "stopped" || wsCloses.Load() != 1 {
					t.Fatalf("err=%v status=%+v WS closes=%d", s.Err(), s.Status(), wsCloses.Load())
				}
				rebound, err := net.Listen("tcp", s.Addr())
				if err != nil {
					t.Fatalf("shutdown did not release the listening address: %v", err)
				}
				_ = rebound.Close()
			})
		}
	}
}

func TestListenerCloseRunsOnceAndPreservesFirstError(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("underlying listener close failed")
	underlying := &failingCloseListener{Listener: raw, failure: errors.Join(net.ErrClosed, sentinel)}
	listener := &onceCloseListener{Listener: underlying}
	t.Cleanup(func() { _ = listener.Close() })
	var callers sync.WaitGroup
	for range 32 {
		callers.Go(func() {
			if err := listener.Close(); !errors.Is(err, sentinel) || !errors.Is(err, net.ErrClosed) {
				t.Errorf("first close error lost: %v", err)
			}
		})
	}
	callers.Wait()
	if got := underlying.closes.Load(); got != 1 {
		t.Fatalf("underlying Close called %d times, want one", got)
	}
}

func TestShutdownPreservesListenerErrorAfterServeExits(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("underlying listener close failed")
	underlying := &failingCloseListener{Listener: raw, failure: sentinel}
	listener := &onceCloseListener{Listener: underlying}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("first close error = %v", err)
	}
	server := &http.Server{Handler: http.NotFoundHandler()}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	// Ensure Serve has already unregistered the listener. Shutdown can no
	// longer collect its error; the adapter must retain its own Close result.
	select {
	case err := <-serveResult:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve on closed listener: %v", err)
		}
		serveResult <- err
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not exit")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{http: server, listener: listener, cfg: Config{ShutdownTimeout: time.Second}, cancel: cancel, done: make(chan struct{})}
	go s.run(runCtx, serveResult)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.Close(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("adapter lost listener close error: %v", err)
	}
	if !errors.Is(s.Err(), sentinel) || underlying.closes.Load() != 1 {
		t.Fatalf("Err=%v underlying closes=%d", s.Err(), underlying.closes.Load())
	}
}

type failingCloseListener struct {
	net.Listener
	failure error
	closes  atomic.Int64
}

func (l *failingCloseListener) Close() error {
	l.closes.Add(1)
	return errors.Join(l.Listener.Close(), l.failure)
}

// BaseContext runs after net/http registers its listener. OnShutdown runs after
// it attempts to close that listener. These gates force the duplicate-close
// window without sleeps, including the TLS listener wrapper used by ACME.
func holdHTTPServeUntilShutdown(t *testing.T, server *http.Server) (<-chan struct{}, func()) {
	t.Helper()
	registered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseServe := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseServe)
	server.BaseContext = func(net.Listener) context.Context {
		close(registered)
		<-release
		return context.Background()
	}
	server.RegisterOnShutdown(releaseServe)
	return registered, releaseServe
}
