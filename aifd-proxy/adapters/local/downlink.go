package local

import (
	"context"
	"fmt"
	"sync"
	"time"

	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

type pushSource interface {
	SubscribePush(int64, func(context.Context, rpc.Message) error) (func(), error)
}

// Downlink binds local UID PubSub to the proxy's live connection set. It starts no
// polling loop and never replays inspection history as online or offline delivery.
type Downlink struct {
	source        pushSource
	gateway       *proxyruntime.Gateway
	delivery      *proxyruntime.Delivery
	mu            sync.Mutex
	subscriptions map[int64]func()
	closed        bool
}

func NewDownlink(source pushSource, gateway *proxyruntime.Gateway, delivery *proxyruntime.Delivery) *Downlink {
	return &Downlink{source: source, gateway: gateway, delivery: delivery, subscriptions: make(map[int64]func())}
}

func (d *Downlink) SubscribeUID(uid int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.source == nil || d.gateway == nil || d.delivery == nil {
		return fmt.Errorf("local downlink is unavailable")
	}
	if d.subscriptions[uid] != nil {
		return nil
	}
	cancel, err := d.source.SubscribePush(uid, func(ctx context.Context, msg rpc.Message) error {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return d.delivery.Broadcast(sendCtx, d.gateway.ConnectionIDs(uid), msg)
	})
	if err != nil {
		return err
	}
	d.subscriptions[uid] = cancel
	return nil
}

func (d *Downlink) UnsubscribeUID(uid int64) error {
	d.mu.Lock()
	cancel := d.subscriptions[uid]
	delete(d.subscriptions, uid)
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (d *Downlink) Close() error {
	d.mu.Lock()
	d.closed = true
	subscriptions := d.subscriptions
	d.subscriptions = make(map[int64]func())
	d.mu.Unlock()
	for _, cancel := range subscriptions {
		cancel()
	}
	return nil
}
