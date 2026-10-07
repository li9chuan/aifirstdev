package app

import (
	"context"
	"sync"
	"time"

	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

// LocalDownlinkBridge 把 local bus 记录的 Push 投递到 proxy Delivery，仅用于 minigame 示例。
type LocalDownlinkBridge struct {
	bus      *rpc.LocalBus
	gateway  *proxyruntime.Gateway
	delivery *proxyruntime.Delivery

	mu         sync.Mutex
	subscribed map[int64]struct{}
	next       int
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
}

// NewLocalDownlinkBridge 创建并启动 local push 轮询桥。
func NewLocalDownlinkBridge(bus *rpc.LocalBus, gateway *proxyruntime.Gateway, delivery *proxyruntime.Delivery) *LocalDownlinkBridge {
	bridge := &LocalDownlinkBridge{
		bus:        bus,
		gateway:    gateway,
		delivery:   delivery,
		subscribed: make(map[int64]struct{}),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go bridge.run()
	return bridge
}

// SubscribeUID 标记 uid 需要接收 local bus 下行消息。
func (b *LocalDownlinkBridge) SubscribeUID(uid int64) error {
	b.mu.Lock()
	b.subscribed[uid] = struct{}{}
	b.mu.Unlock()
	return nil
}

// UnsubscribeUID 取消 uid 的 local bus 下行订阅。
func (b *LocalDownlinkBridge) UnsubscribeUID(uid int64) error {
	b.mu.Lock()
	delete(b.subscribed, uid)
	b.mu.Unlock()
	return nil
}

// Close 停止轮询桥。
func (b *LocalDownlinkBridge) Close() error {
	if b == nil {
		return nil
	}
	b.stopOnce.Do(func() {
		close(b.stop)
		<-b.done
	})
	return nil
}

func (b *LocalDownlinkBridge) run() {
	defer close(b.done)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.drain(context.Background())
		}
	}
}

func (b *LocalDownlinkBridge) drain(ctx context.Context) {
	if b == nil || b.bus == nil || b.gateway == nil || b.delivery == nil {
		return
	}
	pushes := b.bus.Pushes()
	b.mu.Lock()
	start := b.next
	if start > len(pushes) {
		start = len(pushes)
	}
	b.next = len(pushes)
	subscribed := make(map[int64]struct{}, len(b.subscribed))
	for uid := range b.subscribed {
		subscribed[uid] = struct{}{}
	}
	b.mu.Unlock()
	for _, push := range pushes[start:] {
		if _, ok := subscribed[push.UID]; !ok {
			continue
		}
		_ = b.delivery.Broadcast(ctx, b.gateway.ConnectionIDs(push.UID), push.Message)
	}
}
