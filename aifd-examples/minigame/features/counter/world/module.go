package world

import (
	"context"
	"fmt"
	"sync"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

const worldStateKey = "minigame:world:counter"

// Module 保存 counter 的全局统计，用于展示 world module 恢复和内部 RPC。
type Module struct {
	mu        sync.RWMutex
	db        *rdb.DB
	saver     rdb.SetSaver[*pbmsg.CounterWorldState]
	observe   observe.Sink
	recovered bool
	started   bool
}

// Option 配置 counter world module。
type Option func(*options)

type options struct {
	db *rdb.DB
}

// WithDB 让 counter world module 绑定显式 DB。
func WithDB(db *rdb.DB) Option {
	return func(opts *options) {
		opts.db = db
	}
}

// NewModule 创建 counter world module。
func NewModule(sink observe.Sink, opts ...Option) *Module {
	var cfg options
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Module{db: cfg.db, observe: sink}
}

// Descriptor 返回 counter world module 描述。
func (m *Module) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{
		ID:      "minigame.counter.world",
		Version: "v0.1.0",
		Kind:    base.ModuleKindService,
		Provides: []base.Capability{
			{Kind: "rpc", Name: counter.TopicGlobalCounterAdd},
		},
	}
}

// Recover 在 RPC 可写入前恢复 world 统计状态。
func (m *Module) Recover(ctx context.Context) error {
	if err := m.bindSaver(); err != nil {
		return err
	}
	ok, err := m.saver.Load(ctx)
	if err != nil {
		return err
	}
	if !ok {
		if err := m.saver.Save(ctx); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.recovered = true
	m.mu.Unlock()
	m.record(ctx, "counter.world.recover", map[string]string{"loaded": fmt.Sprintf("%v", ok)})
	return nil
}

func (m *Module) bindSaver() error {
	if m.db != nil {
		return m.saver.BindDB(m.db, worldStateKey, &pbmsg.CounterWorldState{})
	}
	return m.saver.Bind(worldStateKey, &pbmsg.CounterWorldState{})
}

// RegisterRPC 注册 game host 到 world host 的内部统计 route。
func (m *Module) RegisterRPC(reg *rpc.Registry) error {
	return rpc.Server(reg, counter.TopicGlobalCounterAdd, func(s *rpc.Session, req *pbmsg.CounterWorldAdd) {
		if err := m.add(s.Context(), req); err != nil {
			s.Error(rpc.CodeInternal, err.Error())
		}
	})
}

// Start 标记 world module 已启动；真实业务可以在这里启动 tick。
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.recovered {
		return fmt.Errorf("counter world not recovered")
	}
	m.started = true
	m.record(ctx, "counter.world.start", nil)
	return nil
}

// Close 停止 world module。
func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
	m.record(ctx, "counter.world.close", nil)
	return nil
}

// Snapshot 返回 world 统计状态的深拷贝。
func (m *Module) Snapshot() *pbmsg.CounterWorldState {
	m.saver.RLock()
	defer m.saver.RUnlock()
	if m.saver.DbProto == nil {
		return &pbmsg.CounterWorldState{}
	}
	return proto.Clone(m.saver.DbProto).(*pbmsg.CounterWorldState)
}

func (m *Module) add(ctx context.Context, req *pbmsg.CounterWorldAdd) error {
	m.mu.RLock()
	recovered := m.recovered
	m.mu.RUnlock()
	if !recovered {
		return fmt.Errorf("counter world not recovered")
	}
	if req.GetDelta() <= 0 {
		return nil
	}
	m.saver.Lock()
	if m.saver.DbProto == nil {
		m.saver.DbProto = &pbmsg.CounterWorldState{}
	}
	m.saver.DbProto.TotalAdd += int64(req.GetDelta())
	m.saver.DbProto.LastUid = req.GetUid()
	err := m.saver.SaveUnsafe(ctx)
	m.saver.Unlock()
	if err != nil {
		return err
	}
	m.record(ctx, "counter.world.add", map[string]string{"uid": fmt.Sprintf("%d", req.GetUid())})
	return nil
}

func (m *Module) record(ctx context.Context, name string, dimensions map[string]string) {
	if m == nil || m.observe == nil {
		return
	}
	_ = m.observe.Write(ctx, []observe.Event{observe.Metric(name, 1, dimensions)})
}
