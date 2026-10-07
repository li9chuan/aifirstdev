package local

import (
	"context"
	"sync"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
)

// UpstreamRecorder 捕获转发后的客户端消息，用于测试。
type UpstreamRecorder struct {
	mu        sync.Mutex
	envelopes []proxy.Envelope
}

// NewUpstreamRecorder 创建空的上游记录器。
func NewUpstreamRecorder() *UpstreamRecorder {
	return &UpstreamRecorder{}
}

// Forward 记录一个已认证上行 Envelope。
func (r *UpstreamRecorder) Forward(_ context.Context, env proxy.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envelopes = append(r.envelopes, env)
	return nil
}

// Envelopes 返回已记录上行 Envelope 的快照。
func (r *UpstreamRecorder) Envelopes() []proxy.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]proxy.Envelope, len(r.envelopes))
	copy(out, r.envelopes)
	return out
}
