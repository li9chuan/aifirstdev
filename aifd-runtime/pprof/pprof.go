package pprof

import (
	"context"
	"fmt"
	"net"
	"net/http"
	stdpprof "net/http/pprof"
	"time"
)

const defaultShutdownTimeout = 30 * time.Second

// Options controls optional pprof diagnostics hooks.
type Options struct {
	OnError func(error)
}

// Start 启动可选 pprof HTTP server；addr 为空时不做任何事。
func Start(ctx context.Context, addr string) (*http.Server, error) {
	return StartWithOptions(ctx, addr, Options{})
}

// StartWithOptions 启动可选 pprof HTTP server，并允许调用方接收后台 serve 错误。
func StartWithOptions(ctx context.Context, addr string, opts Options) (*http.Server, error) {
	if addr == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", stdpprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", stdpprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", stdpprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", stdpprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", stdpprof.Trace)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pprof listen %s: %w", addr, err)
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		serve(server, listener, opts.OnError)
	}()
	return server, nil
}

func serve(server *http.Server, listener net.Listener, onError func(error)) {
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		if onError != nil {
			onError(err)
		}
	}
}
