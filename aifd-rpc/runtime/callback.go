package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

const defaultCallbackTimeout = 15 * time.Second

// ErrCallbackCacheClosed 表示在 Close 后继续添加 callback。
var ErrCallbackCacheClosed = errors.New("rpc callback cache closed")

// CallbackEntry 描述一个等待中的 request/reply callback 及其清理 hook。
type CallbackEntry struct {
	Inbox       string
	RouteName   string
	UID         int64
	ClientInbox int
	Handler     rpc.Handler
	OnTimeout   func(context.Context) error
	Cleanup     func()
	Timeout     time.Duration
}

type callbackItem struct {
	entry CallbackEntry
	timer *time.Timer
}

// CallbackCache 跟踪 request/reply inbox callback 和 timeout callback。
type CallbackCache struct {
	mu             sync.Mutex
	defaultTimeout time.Duration
	entries        map[string]*callbackItem
	closed         bool
	seq            atomic.Uint64
}

// NewCallbackCache 创建带默认条目超时的 callback cache。
func NewCallbackCache(defaultTimeout time.Duration) *CallbackCache {
	if defaultTimeout <= 0 {
		defaultTimeout = defaultCallbackTimeout
	}
	return &CallbackCache{
		defaultTimeout: defaultTimeout,
		entries:        make(map[string]*callbackItem),
	}
}

// NextInbox 为 request/reply callback 构造唯一 inbox 名称。
func (c *CallbackCache) NextInbox(prefix string) string {
	if prefix == "" {
		prefix = "_INBOX."
	}
	return prefix + time.Now().Format("20060102150405") + "." + formatUint(c.seq.Add(1))
}

// Add 存储 callback 条目，并启动其超时清理。
func (c *CallbackCache) Add(entry CallbackEntry) error {
	if entry.Inbox == "" {
		entry.Inbox = c.NextInbox("")
	}
	timeout := entry.Timeout
	if timeout <= 0 {
		timeout = c.defaultTimeout
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCallbackCacheClosed
	}
	timer := time.AfterFunc(timeout, func() {
		c.timeout(entry.Inbox)
	})
	c.entries[entry.Inbox] = &callbackItem{entry: entry, timer: timer}
	return nil
}

// Resolve 消费一个 callback 条目，并用克隆后的消息调用 handler。
func (c *CallbackCache) Resolve(ctx context.Context, inbox string, session rpc.Session, msg rpc.Message) (bool, error) {
	item, ok := c.take(inbox)
	if !ok {
		return false, nil
	}
	if item.entry.Cleanup != nil {
		item.entry.Cleanup()
	}
	if item.entry.Handler == nil {
		return true, nil
	}
	return true, item.entry.Handler(ctx, session, msg.Clone())
}

// Cancel 移除一个等待中的 callback，并执行其清理 hook。
func (c *CallbackCache) Cancel(inbox string) bool {
	item, ok := c.take(inbox)
	if !ok {
		return false
	}
	if item.entry.Cleanup != nil {
		item.entry.Cleanup()
	}
	return true
}

// Close 取消全部等待中的 callback，并阻止后续添加。
func (c *CallbackCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for inbox, item := range c.entries {
		item.timer.Stop()
		if item.entry.Cleanup != nil {
			item.entry.Cleanup()
		}
		delete(c.entries, inbox)
	}
}

func (c *CallbackCache) take(inbox string) (*callbackItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[inbox]
	if !ok {
		return nil, false
	}
	item.timer.Stop()
	delete(c.entries, inbox)
	return item, true
}

func (c *CallbackCache) timeout(inbox string) {
	item, ok := c.take(inbox)
	if !ok {
		return
	}
	if item.entry.Cleanup != nil {
		item.entry.Cleanup()
	}
	if item.entry.OnTimeout != nil {
		_ = item.entry.OnTimeout(context.Background())
	}
}

func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
