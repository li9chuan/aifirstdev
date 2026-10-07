package client

import (
	"container/list"
	"sync"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

type callbackHandler func(rpc.Message, error) error

type callbackItem struct {
	id       string
	handler  callbackHandler
	expireAt time.Time
	node     *list.Element
}

type callbackCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*callbackItem
	queue   *list.List
	notify  chan struct{}
}

func newCallbackCache(ttl time.Duration) *callbackCache {
	if ttl <= 0 {
		ttl = defaultTimeout
	}
	return &callbackCache{
		ttl:     ttl,
		entries: make(map[string]*callbackItem),
		queue:   list.New(),
		notify:  make(chan struct{}, 1),
	}
}

func (c *callbackCache) Add(id string, handler callbackHandler) {
	if id == "" || handler == nil {
		return
	}
	c.mu.Lock()
	if old, ok := c.entries[id]; ok {
		c.removeLocked(old)
	}
	item := &callbackItem{id: id, handler: handler, expireAt: time.Now().Add(c.ttl)}
	item.node = c.queue.PushBack(item)
	c.entries[id] = item
	c.mu.Unlock()
	c.signal()
}

func (c *callbackCache) Pop(id string) (callbackHandler, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	c.removeLocked(item)
	return item.handler, true
}

func (c *callbackCache) PopExpired(now time.Time) []callbackHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	var handlers []callbackHandler
	for {
		front := c.queue.Front()
		if front == nil {
			break
		}
		item := front.Value.(*callbackItem)
		if now.Before(item.expireAt) {
			break
		}
		c.removeLocked(item)
		handlers = append(handlers, item.handler)
	}
	return handlers
}

func (c *callbackCache) Clear() []callbackHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	handlers := make([]callbackHandler, 0, len(c.entries))
	for c.queue.Len() > 0 {
		item := c.queue.Front().Value.(*callbackItem)
		c.removeLocked(item)
		handlers = append(handlers, item.handler)
	}
	return handlers
}

func (c *callbackCache) NextExpire() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	front := c.queue.Front()
	if front == nil {
		return time.Time{}, false
	}
	return front.Value.(*callbackItem).expireAt, true
}

func (c *callbackCache) Notify() <-chan struct{} {
	return c.notify
}

func (c *callbackCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *callbackCache) removeLocked(item *callbackItem) {
	delete(c.entries, item.id)
	if item.node != nil {
		c.queue.Remove(item.node)
		item.node = nil
	}
}

func (c *callbackCache) signal() {
	select {
	case c.notify <- struct{}{}:
	default:
	}
}
