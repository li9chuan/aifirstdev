package entrypoint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const defaultShutdownTimeout = 30 * time.Second

const (
	defaultHTTPReadHeaderTimeout = 5 * time.Second
	defaultHTTPReadTimeout       = 15 * time.Second
	defaultHTTPWriteTimeout      = 30 * time.Second
	defaultHTTPIdleTimeout       = 60 * time.Second
	defaultHTTPMaxHeaderBytes    = 1 << 20
)

// HTTPServerProfile defines the bounded connection defaults used by public HTTP entrypoints.
type HTTPServerProfile struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// DefaultHTTPServerProfile returns the framework HTTP connection limits.
func DefaultHTTPServerProfile() HTTPServerProfile {
	return HTTPServerProfile{
		ReadHeaderTimeout: defaultHTTPReadHeaderTimeout,
		ReadTimeout:       defaultHTTPReadTimeout,
		WriteTimeout:      defaultHTTPWriteTimeout,
		IdleTimeout:       defaultHTTPIdleTimeout,
		MaxHeaderBytes:    defaultHTTPMaxHeaderBytes,
	}
}

// NewHTTPServer builds an HTTP server using the framework connection limits.
func NewHTTPServer(handler http.Handler) *http.Server {
	profile := DefaultHTTPServerProfile()
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: profile.ReadHeaderTimeout,
		ReadTimeout:       profile.ReadTimeout,
		WriteTimeout:      profile.WriteTimeout,
		IdleTimeout:       profile.IdleTimeout,
		MaxHeaderBytes:    profile.MaxHeaderBytes,
	}
}

// Component 是服务退出时需要关闭的资源。
type Component struct {
	Name  string
	Close func(context.Context) error
}

// SignalContext 创建监听 SIGINT/SIGTERM 的退出 context。
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return SignalContextFor(parent, syscall.SIGINT, syscall.SIGTERM)
}

// SignalContextFor 创建监听指定 signal 的退出 context。
func SignalContextFor(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if len(signals) == 0 {
		return context.WithCancel(parent)
	}
	return signal.NotifyContext(parent, signals...)
}

// Close 按注册顺序的反向关闭组件。
func Close(ctx context.Context, components ...Component) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for i := len(components) - 1; i >= 0; i-- {
		component := components[i]
		if component.Close == nil {
			continue
		}
		if err := component.Close(ctx); err != nil {
			if component.Name != "" {
				err = fmt.Errorf("%s close: %w", component.Name, err)
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ListenAndServe 随 ctx 退出而优雅关闭 HTTP server。
func ListenAndServe(ctx context.Context, server *http.Server) error {
	if server == nil {
		return fmt.Errorf("http server is nil")
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
