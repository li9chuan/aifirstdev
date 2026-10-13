package runtime

import (
	"context"
	"errors"
	"sync"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Delivery 跟踪用于下行消息的存活传输连接。
type Delivery struct {
	mu    sync.RWMutex
	conns map[string]proxy.Connection
}

// NewDelivery 创建空的存活连接投递注册表。
func NewDelivery() *Delivery {
	return &Delivery{conns: make(map[string]proxy.Connection)}
}

// Add 按 ID 注册一个连接，并拒绝重复所有权。
func (d *Delivery) Add(conn proxy.Connection) error {
	if conn == nil || conn.ID() == "" {
		return ErrConnectionNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.conns[conn.ID()]; exists {
		return ErrConnectionExists
	}
	d.conns[conn.ID()] = conn
	return nil
}

// Remove 注销一个连接 ID，并返回其是否存在。
func (d *Delivery) Remove(connID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.conns[connID]; !ok {
		return false
	}
	delete(d.conns, connID)
	return true
}

// Close 关闭并注销一个连接 ID。
func (d *Delivery) Close(ctx context.Context, connID string) error {
	d.mu.Lock()
	conn, ok := d.conns[connID]
	if ok {
		delete(d.conns, connID)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	return conn.Close(ctx)
}

// Send 克隆并投递一条下行消息到已注册连接。
func (d *Delivery) Send(ctx context.Context, connID string, msg rpc.Message) error {
	d.mu.RLock()
	conn, ok := d.conns[connID]
	d.mu.RUnlock()
	if !ok {
		return ErrConnectionNotFound
	}
	return conn.Send(ctx, cloneMessage(msg))
}

// Broadcast 向每个连接 ID 发送同一条消息，并合并失败。
func (d *Delivery) Broadcast(ctx context.Context, connIDs []string, msg rpc.Message) error {
	var joined error
	for _, connID := range connIDs {
		if err := d.Send(ctx, connID, msg); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// CloseAll also closes connections that have not authenticated yet.
func (d *Delivery) CloseAll(ctx context.Context) error {
	d.mu.Lock()
	current := d.conns
	d.conns = make(map[string]proxy.Connection)
	d.mu.Unlock()
	var joined error
	for _, conn := range current {
		joined = errors.Join(joined, conn.Close(ctx))
	}
	return joined
}
