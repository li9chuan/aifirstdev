package app

import (
	"context"
	"net/http"

	"github.com/li9chuan/aifirstdev/aifd-runtime/entrypoint"
)

// NewWebSocketHTTPServer mounts WS and the development-only account endpoint.
func NewWebSocketHTTPServer(addr string, handler http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	mux.Handle("/demo/auth", handler)
	return &http.Server{Addr: addr, Handler: mux}
}

// ServeHTTP 随 ctx 退出而优雅关闭 HTTP server。
func ServeHTTP(ctx context.Context, server *http.Server) error {
	return entrypoint.ListenAndServe(ctx, server)
}
