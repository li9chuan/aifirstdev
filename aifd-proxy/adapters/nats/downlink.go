package nats

import (
	"context"
	"errors"
	"strconv"
	"sync"

	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	natsgo "github.com/nats-io/nats.go"
)

var (
	// ErrDownlinkMissing 表示 NATS downlink 缺少必要依赖。
	ErrDownlinkMissing = errors.New("proxy nats downlink dependency is missing")
	// ErrInvalidUID 表示无法为非法 uid 建立下行订阅。
	ErrInvalidUID = errors.New("proxy nats downlink uid is invalid")
)

// Config 控制 NATS 下行 subject 约定。
type Config struct {
	ClientSubjectPrefix string
}

// Downlink 订阅 S.<uid> 下行消息并投递到 proxy 连接。
type Downlink struct {
	nc       *natsgo.Conn
	gateway  *proxyruntime.Gateway
	delivery *proxyruntime.Delivery
	prefix   string

	mu   sync.Mutex
	subs map[int64]*natsgo.Subscription
}

// NewDownlink 创建 NATS 下行订阅器。
func NewDownlink(nc *natsgo.Conn, gateway *proxyruntime.Gateway, delivery *proxyruntime.Delivery, cfg Config) *Downlink {
	if cfg.ClientSubjectPrefix == "" {
		cfg.ClientSubjectPrefix = "S."
	}
	return &Downlink{
		nc:       nc,
		gateway:  gateway,
		delivery: delivery,
		prefix:   cfg.ClientSubjectPrefix,
		subs:     make(map[int64]*natsgo.Subscription),
	}
}

// SubscribeUID 订阅指定 uid 的下行 subject。
func (d *Downlink) SubscribeUID(uid int64) error {
	if uid <= 0 {
		return ErrInvalidUID
	}
	if d == nil || d.nc == nil || d.gateway == nil || d.delivery == nil {
		return ErrDownlinkMissing
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.subs[uid] != nil {
		return nil
	}
	sub, err := d.nc.Subscribe(d.subject(uid), func(msg *natsgo.Msg) {
		d.handle(uid, msg)
	})
	if err != nil {
		return err
	}
	d.subs[uid] = sub
	return nil
}

// UnsubscribeUID 取消指定 uid 的下行订阅。
func (d *Downlink) UnsubscribeUID(uid int64) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	sub := d.subs[uid]
	delete(d.subs, uid)
	d.mu.Unlock()
	if sub == nil {
		return nil
	}
	return sub.Unsubscribe()
}

// Close 取消所有下行订阅。
func (d *Downlink) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	subs := d.subs
	d.subs = make(map[int64]*natsgo.Subscription)
	d.mu.Unlock()
	var joined error
	for _, sub := range subs {
		if sub != nil {
			joined = errors.Join(joined, sub.Unsubscribe())
		}
	}
	return joined
}

func (d *Downlink) handle(uid int64, nmsg *natsgo.Msg) {
	msg := messageFromNATS(nmsg)
	ids := d.gateway.ConnectionIDs(uid)
	if len(ids) == 0 {
		return
	}
	_ = d.delivery.Broadcast(context.Background(), ids, msg)
}

func (d *Downlink) subject(uid int64) string {
	return d.prefix + strconv.FormatInt(uid, 10)
}

func messageFromNATS(nmsg *natsgo.Msg) rpc.Message {
	header := make(map[string]string, len(nmsg.Header))
	for key, values := range nmsg.Header {
		if len(values) > 0 {
			header[key] = values[0]
		}
	}
	name := header[rpc.HeaderResponse]
	if name == "" {
		name = header[rpc.HeaderPBType]
	}
	return rpc.Message{
		Name:    name,
		Payload: append([]byte(nil), nmsg.Data...),
		Header:  header,
	}
}
