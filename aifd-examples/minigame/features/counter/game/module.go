package game

import (
	"context"
	"fmt"
	"strconv"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeroot "github.com/li9chuan/aifirstdev/aifd-runtime/root"
)

const defaultLimit int32 = 10

// Config 提供 counter 运行期配置解释后的结果。
type Config interface {
	CounterLimit() int32
}

// Module 是 minigame 的计数器 game module。
type Module struct {
	moduleCtx runtimeroot.ModuleContext[*pbmsg.DBPlayer]
	config    Config
	observe   observe.Sink
}

// Options 配置 counter game module。
type Options struct {
	Config  Config
	Observe observe.Sink
}

// NewModule 创建 counter game module。
func NewModule(opts Options) *Module {
	return &Module{config: opts.Config, observe: opts.Observe}
}

// BindModuleContext 绑定 runtime 提供的 active owner 查询入口。
func (m *Module) BindModuleContext(moduleCtx runtimeroot.ModuleContext[*pbmsg.DBPlayer]) error {
	if m == nil {
		return fmt.Errorf("counter module is nil")
	}
	if !moduleCtx.HasRootLookup() {
		return fmt.Errorf("counter root lookup is nil")
	}
	m.moduleCtx = moduleCtx
	return nil
}

// Descriptor 返回 counter game module 描述。
func (m *Module) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{
		ID:      "minigame.counter.game",
		Version: "v0.1.0",
		Kind:    base.ModuleKindRoot,
		Provides: []base.Capability{
			{Kind: "rpc", Name: counter.TopicMceCounterAdd},
		},
	}
}

// OnLoad 确保业务 DBPlayer 中存在 counter 子状态。
func (m *Module) OnLoad(ctx context.Context, owner *runtimeroot.Root[*pbmsg.DBPlayer]) error {
	var missing bool
	if err := owner.Read(func(db *pbmsg.DBPlayer) {
		missing = db.GetCounter() == nil
	}); err != nil {
		return err
	}
	if !missing {
		return nil
	}
	return owner.Mutate(ctx, func(db *pbmsg.DBPlayer) error {
		if db.Counter == nil {
			db.Counter = &pbmsg.CounterState{}
		}
		return nil
	})
}

// OnActivate 当前示例没有 owner 激活侧状态推进。
func (m *Module) OnActivate(context.Context, *runtimeroot.Root[*pbmsg.DBPlayer]) error {
	return nil
}

// RegisterRPC 注册客户端 counter 请求。
func (m *Module) RegisterRPC(reg *rpc.Registry) error {
	if m == nil || !m.moduleCtx.HasRootLookup() {
		return fmt.Errorf("counter module context is not bound")
	}
	return rpc.Client(reg, counter.TopicMceCounterAdd, func(s *rpc.Session, req *pbmsg.MceCounterAdd) {
		m.handleAdd(s, req)
	})
}

func (m *Module) handleAdd(s *rpc.Session, req *pbmsg.MceCounterAdd) {
	uid := s.UID()
	limit := m.counterLimit()
	owner, ok := m.owner(uid)
	if !ok {
		m.record(s.Context(), uid, "offline")
		s.ResponseAs(counter.TopicMseCounterState, &pbmsg.MseCounterState{
			Uid:     uid,
			Limit:   limit,
			Message: "player offline",
		})
		return
	}
	delta := req.GetDelta()
	if delta <= 0 {
		m.respondCurrent(s, owner, limit, "invalid delta")
		m.record(s.Context(), uid, "invalid")
		return
	}

	var value int32
	result := "ok"
	if err := owner.Mutate(s.Context(), func(db *pbmsg.DBPlayer) error {
		if db.Counter == nil {
			db.Counter = &pbmsg.CounterState{}
		}
		next := db.Counter.Value + delta
		if next >= limit {
			next = limit
			result = "limit"
		}
		db.Counter.Value = next
		value = next
		return nil
	}); err != nil {
		m.record(s.Context(), uid, "mutate_error")
		s.ResponseAs(counter.TopicMseCounterState, &pbmsg.MseCounterState{
			Uid:     uid,
			Limit:   limit,
			Message: err.Error(),
		})
		return
	}

	m.record(s.Context(), uid, result)
	s.Tell(counter.TopicGlobalCounterAdd, &pbmsg.CounterWorldAdd{Uid: uid, Delta: delta})
	s.ResponseAs(counter.TopicMseCounterState, &pbmsg.MseCounterState{
		Uid:     uid,
		Value:   value,
		Limit:   limit,
		Message: result,
	})
}

func (m *Module) respondCurrent(s *rpc.Session, owner *runtimeroot.Root[*pbmsg.DBPlayer], limit int32, message string) {
	var value int32
	_ = owner.Read(func(db *pbmsg.DBPlayer) {
		value = db.GetCounter().GetValue()
	})
	s.ResponseAs(counter.TopicMseCounterState, &pbmsg.MseCounterState{
		Uid:     owner.ID(),
		Value:   value,
		Limit:   limit,
		Message: message,
	})
}

func (m *Module) owner(uid int64) (*runtimeroot.Root[*pbmsg.DBPlayer], bool) {
	if m == nil || !m.moduleCtx.HasRootLookup() {
		return nil, false
	}
	return m.moduleCtx.GetRoot(uid)
}

func (m *Module) counterLimit() int32 {
	if m == nil || m.config == nil {
		return defaultLimit
	}
	limit := m.config.CounterLimit()
	if limit <= 0 {
		return defaultLimit
	}
	return limit
}

func (m *Module) record(ctx context.Context, uid int64, result string) {
	if m == nil || m.observe == nil {
		return
	}
	_ = m.observe.Write(ctx, []observe.Event{observe.Metric("counter.add", 1, map[string]string{
		"uid":    strconv.FormatInt(uid, 10),
		"result": result,
	})})
}
