package local

import (
	"context"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Connection 是用于 proxy 测试的内存客户端连接。
type Connection struct {
	id     string
	mu     sync.Mutex
	sent   []rpc.Message
	closed bool
}

// NewConnection 创建带稳定连接 ID 的内存连接。
func NewConnection(id string) *Connection {
	return &Connection{id: id}
}

// ID 返回 proxy 注册表使用的连接标识。
func (c *Connection) ID() string {
	return c.id
}

// Send 记录一条克隆后的出站消息，便于后续检查。
func (c *Connection) Send(_ context.Context, msg rpc.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, cloneMessage(msg))
	return nil
}

// Close 将内存连接标记为已关闭。
func (c *Connection) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// Sent 返回 Send 记录的出站消息快照。
func (c *Connection) Sent() []rpc.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]rpc.Message, len(c.sent))
	copy(out, c.sent)
	return out
}

// Closed 返回 Close 是否已被调用。
func (c *Connection) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func cloneMessage(msg rpc.Message) rpc.Message {
	out := msg
	if len(msg.Payload) > 0 {
		out.Payload = append([]byte(nil), msg.Payload...)
	}
	if len(msg.Header) > 0 {
		out.Header = make(map[string]string, len(msg.Header))
		for key, value := range msg.Header {
			out.Header[key] = value
		}
	}
	return out
}
