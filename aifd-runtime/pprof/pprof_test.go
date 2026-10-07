package pprof

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartEmptyAddressIsNoop(t *testing.T) {
	server, err := Start(context.Background(), "")
	if err != nil {
		t.Fatalf("Start empty error = %v", err)
	}
	if server != nil {
		t.Fatalf("Start empty server = %#v, want nil", server)
	}
}

func TestServeReportsNonServerClosedError(t *testing.T) {
	want := errors.New("accept failed")
	reported := make(chan error, 1)
	serve(&http.Server{}, failingListener{err: want}, func(err error) {
		reported <- err
	})
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("reported error = nil")
		}
	default:
		t.Fatal("serve did not report non-server-closed error")
	}
}

func TestServeIgnoresServerClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	server := &http.Server{}
	reported := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		serve(server, listener, func(err error) {
			reported <- err
		})
		close(done)
	}()
	if err := server.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after server close")
	}
	select {
	case err := <-reported:
		t.Fatalf("reported error = %v, want none", err)
	default:
	}
}

type failingListener struct {
	err error
}

func (l failingListener) Accept() (net.Conn, error) {
	return nil, l.err
}

func (l failingListener) Close() error {
	return nil
}

func (l failingListener) Addr() net.Addr {
	return testAddr("test")
}

type testAddr string

func (a testAddr) Network() string {
	return string(a)
}

func (a testAddr) String() string {
	return string(a)
}
