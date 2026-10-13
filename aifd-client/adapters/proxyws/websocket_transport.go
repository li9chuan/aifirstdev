package proxyws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
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
)

// WebSocketConfig 配置客户端到 proxy 的 websocket 连接。
type WebSocketConfig struct {
	URL             string
	Header          http.Header
	MaxMessageBytes int64
	DialTimeout     time.Duration
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
	conn, _, err := coderws.Dial(ctx, cfg.URL, &coderws.DialOptions{HTTPHeader: cfg.Header})
	if err != nil {
		return nil, err
	}
	if cfg.MaxMessageBytes > 0 {
		conn.SetReadLimit(cfg.MaxMessageBytes)
	}
	return &WebSocketTransport{conn: conn}, nil
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
