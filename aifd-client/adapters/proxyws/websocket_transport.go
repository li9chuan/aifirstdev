package proxyws

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

var (
	// ErrWebSocketConfig 表示 proxy websocket transport 配置不完整。
	ErrWebSocketConfig = errors.New("proxy websocket transport config is invalid")
	// ErrUnexpectedMessageType 表示 proxy 返回了非 binary frame。
	ErrUnexpectedMessageType = errors.New("proxy websocket transport received non-binary frame")
	// ErrInsecureTransport means the connection or redirect violated TLS policy.
	ErrInsecureTransport = errors.New("proxy websocket transport security policy rejected destination")
)

// WebSocketConfig 配置客户端到 proxy 的 websocket 连接。
type WebSocketConfig struct {
	URL             string
	Header          http.Header
	MaxMessageBytes int64
	DialTimeout     time.Duration
	// HTTPClient optionally supplies trusted roots/proxy settings. It is not mutated.
	HTTPClient *http.Client
	// RequireTLS rejects plaintext URLs. Secure initial URLs always reject downgrade
	// and cross-origin redirects, including redirects modified by a caller hook.
	RequireTLS bool
}

// WebSocketTransport 通过真实 websocket 连接 proxy。
type WebSocketTransport struct {
	conn   *coderws.Conn
	mu     sync.Mutex
	closed atomic.Bool
}

// NewWebSocketTransport 建立客户端到 proxy 的 websocket transport。
func NewWebSocketTransport(ctx context.Context, cfg WebSocketConfig) (*WebSocketTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%w: url is empty", ErrWebSocketConfig)
	}
	if cfg.DialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.DialTimeout)
		defer cancel()
	}
	dialClient, err := websocketHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	conn, _, err := coderws.Dial(ctx, cfg.URL, &coderws.DialOptions{HTTPHeader: cfg.Header, HTTPClient: dialClient})
	baseClient := cfg.HTTPClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	if owned, ok := dialClient.Transport.(*http.Transport); ok {
		original, _ := baseClient.Transport.(*http.Transport)
		if owned != original {
			// Upgraded connections are no longer in the HTTP idle pool. Close
			// leftover redirect/failed-handshake connections on our private clone.
			owned.CloseIdleConnections()
		}
	}
	if err != nil {
		return nil, err
	}
	if cfg.MaxMessageBytes > 0 {
		conn.SetReadLimit(cfg.MaxMessageBytes)
	}
	return &WebSocketTransport{conn: conn}, nil
}

func websocketHTTPClient(cfg WebSocketConfig) (*http.Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid URL", ErrWebSocketConfig)
	}
	secure := cfg.RequireTLS || u.Scheme == "wss" || u.Scheme == "https"
	if (secure && u.Scheme != "wss" && u.Scheme != "https") || (cfg.RequireTLS && u.User != nil) {
		return nil, ErrInsecureTransport
	}
	base := cfg.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if typed, ok := transport.(*http.Transport); secure && ok {
		cloned := typed.Clone()
		if cloned.TLSClientConfig == nil {
			cloned.TLSClientConfig = &tls.Config{}
		}
		if cloned.TLSClientConfig.InsecureSkipVerify {
			return nil, fmt.Errorf("%w: certificate verification is disabled", ErrInsecureTransport)
		}
		if cloned.TLSClientConfig.MaxVersion != 0 && cloned.TLSClientConfig.MaxVersion < tls.VersionTLS12 {
			return nil, fmt.Errorf("%w: TLS 1.2 or newer is required", ErrInsecureTransport)
		}
		if cloned.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			cloned.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
		client.Transport = cloned
	}
	previous := client.CheckRedirect
	check := func(req *http.Request, via []*http.Request) error {
		if secure && (req.URL.Scheme != "https" && req.URL.Scheme != "wss" || !sameOrigin(u, req.URL)) {
			return ErrInsecureTransport
		}
		if secure && req.Host != "" {
			authority := *req.URL
			authority.Host = req.Host
			if !sameOrigin(u, &authority) {
				return ErrInsecureTransport
			}
		}
		if len(via) >= 10 {
			return fmt.Errorf("proxy websocket: stopped after 10 redirects")
		}
		return nil
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := check(req, via); err != nil {
			return err
		}
		if previous != nil {
			if err := previous(req, via); err != nil {
				return err
			}
		}
		return check(req, via)
	}
	return &client, nil
}

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" || u.Scheme == "wss" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// Send 将 rpc.Message 编码为 proxy packet 并写入 binary frame。
func (t *WebSocketTransport) Send(ctx context.Context, msg rpc.Message) error {
	parts, err := proxycodec.EncodeMessage(msg)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "connection closed"}
	}
	return t.conn.Write(ctx, coderws.MessageBinary, bytes.Join(parts, nil))
}

// Recv 读取 proxy binary frame，并解码为 rpc.Message。
func (t *WebSocketTransport) Recv(ctx context.Context) (rpc.Message, error) {
	msgType, data, err := t.conn.Read(ctx)
	if err != nil {
		return rpc.Message{}, err
	}
	if msgType != coderws.MessageBinary {
		return rpc.Message{}, ErrUnexpectedMessageType
	}
	head, body, err := proxycodec.DecodePacket(data)
	if err != nil {
		return rpc.Message{}, err
	}
	return proxycodec.MessageFromPacket(head, body)
}

// Close 幂等关闭 websocket 连接。
func (t *WebSocketTransport) Close(context.Context) error {
	if t == nil || t.conn == nil {
		return nil
	}
	if t.closed.Swap(true) {
		return nil
	}
	return t.conn.CloseNow()
}
