package local

import (
	"context"
	"errors"
	"sync"

	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// ErrTransportClosed 表示使用了已关闭的 local transport。
var ErrTransportClosed = errors.New("local robot transport closed")

// Transport 是供单元测试和示例使用的内存 robot transport。
type Transport struct {
	mu      sync.Mutex
	inbound chan rpc.Message
	sent    []rpc.Message
	closed  bool
}

// NewTransport 创建带缓冲入站队列的内存 robot transport。
func NewTransport() *Transport {
	return &Transport{inbound: make(chan rpc.Message, 32)}
}

// Send 记录克隆后的出站消息。
func (t *Transport) Send(_ context.Context, msg rpc.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrTransportClosed
	}
	t.sent = append(t.sent, cloneMessage(msg))
	return nil
}

// Recv 等待注入的入站消息或 context 取消。
func (t *Transport) Recv(ctx context.Context) (rpc.Message, error) {
	select {
	case <-ctx.Done():
		return rpc.Message{}, ctx.Err()
	case msg, ok := <-t.inbound:
		if !ok {
			return rpc.Message{}, ErrTransportClosed
		}
		return cloneMessage(msg), nil
	}
}

// Close 关闭入站队列，并阻止后续发送或注入。
func (t *Transport) Close(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.inbound)
	return nil
}

// Inject 将克隆后的入站消息放入队列，并返回 transport 是否接受。
func (t *Transport) Inject(msg rpc.Message) bool {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return false
	}
	t.inbound <- cloneMessage(msg)
	return true
}

// Sent 返回克隆后的出站消息快照。
func (t *Transport) Sent() []rpc.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]rpc.Message, len(t.sent))
	copy(out, t.sent)
	return out
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

// TransportFactory 按 agent ID 记录 local transport，供集成测试使用。
type TransportFactory struct {
	mu         sync.Mutex
	transports map[string]*Transport
}

// NewTransportFactory 创建空的 local transport registry。
func NewTransportFactory() *TransportFactory {
	return &TransportFactory{transports: make(map[string]*Transport)}
}

// NewTransport 为 agent spec ID 创建并记录 transport。
func (f *TransportFactory) NewTransport(spec robot.AgentSpec) (robot.Transport, error) {
	transport := NewTransport()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transports[spec.ID] = transport
	return transport, nil
}

// Transport 按 agent ID 返回已记录的 transport。
func (f *TransportFactory) Transport(agentID string) (*Transport, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	transport, ok := f.transports[agentID]
	return transport, ok
}

// Transports 返回按 agent ID 组织的全部已记录 transport 浅拷贝快照。
func (f *TransportFactory) Transports() map[string]*Transport {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]*Transport, len(f.transports))
	for id, transport := range f.transports {
		out[id] = transport
	}
	return out
}
