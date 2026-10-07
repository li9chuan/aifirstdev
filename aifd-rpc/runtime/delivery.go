package runtime

import (
	"context"
	"fmt"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Delivery 是由 bus adapter 创建的可执行 handler 调用。
type Delivery struct {
	route   rpc.Route
	session rpc.Session
	message rpc.Message
	stats   *StatsCollector
	done    func()
}

// NewDelivery 创建 handler 调用，并克隆入站消息 payload。
func NewDelivery(route rpc.Route, session rpc.Session, msg rpc.Message, stats *StatsCollector, done ...func()) *Delivery {
	var onDone func()
	if len(done) > 0 {
		onDone = done[0]
	}
	return &Delivery{
		route:   route,
		session: session,
		message: msg.Clone(),
		stats:   stats,
		done:    onDone,
	}
}

// Session 返回附加到该 delivery 的请求 session。
func (d *Delivery) Session() rpc.Session {
	return d.session
}

// Message 返回 delivery 消息的克隆。
func (d *Delivery) Message() rpc.Message {
	return d.message.Clone()
}

// Route 返回该 delivery 对应的已注册 route 元数据。
func (d *Delivery) Route() rpc.Route {
	return d.route
}

// Handle 调用 route handler，记录 handler 延迟，并将 panic 转为错误，避免单个坏请求杀死 runner worker。
func (d *Delivery) Handle(ctx context.Context) (err error) {
	if d == nil {
		return fmt.Errorf("rpc delivery is nil")
	}
	if d.done != nil {
		defer d.done()
	}
	if d.route.Handler == nil {
		return fmt.Errorf("rpc route %s handler is nil", d.route.Name)
	}
	if d.route.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.route.Timeout)
		defer cancel()
	}
	start := time.Now()
	defer func() {
		if d.stats != nil {
			d.stats.RecordHandle(d.route.Name, time.Since(start))
		}
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("rpc route %s panic: %v", d.route.Name, recovered)
		}
	}()
	return d.route.Handler(ctx, d.session, d.message.Clone())
}
