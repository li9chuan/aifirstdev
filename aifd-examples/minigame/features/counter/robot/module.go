package robot

import (
	"context"
	"fmt"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	robotc "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

// AuthResult 提供登录模块的完成状态。
type AuthResult interface {
	Result() (pbmsg.MseAuth, bool)
}

// ModuleOptions 配置 counter robot 模块。
type ModuleOptions struct {
	Auth  AuthResult
	Delta int32
}

// CounterModule 是 counter 功能的 robot 侧业务模块。
type CounterModule struct {
	opts ModuleOptions

	mu     sync.Mutex
	sent   bool
	done   chan struct{}
	once   sync.Once
	result pbmsg.MseCounterState
	got    bool
	err    error
}

// NewModule 创建 counter robot 模块。
func NewModule(opts ModuleOptions) *CounterModule {
	if opts.Delta == 0 {
		opts.Delta = 1
	}
	return &CounterModule{opts: opts, done: make(chan struct{})}
}

// Name 返回模块名。
func (m *CounterModule) Name() string {
	return "counter"
}

// OnAttach 当前不注册 push；counter 请求回包只通过 Call callback 匹配。
func (m *CounterModule) OnAttach(_ context.Context, _ robotc.Agent) error {
	return nil
}

// OnLogin 不立即发包；counter 要等 auth 成功后由 Tick 触发。
func (m *CounterModule) OnLogin(context.Context, robotc.Agent) error {
	return nil
}

// Tick 在登录成功后发送一次 counter 请求。
func (m *CounterModule) Tick(_ context.Context, agent robotc.Agent) error {
	if !m.authOK() || m.markSent() {
		return nil
	}
	return agent.Call(
		&pbmsg.MceCounterAdd{Delta: m.opts.Delta},
		func(rsp *pbmsg.MseCounterState, err error) {
			if err != nil {
				m.complete(pbmsg.MseCounterState{}, err)
				return
			}
			m.complete(*rsp, nil)
		},
	)
}

// Actions 当前示例不注册行为树 action。
func (m *CounterModule) Actions() []robotc.ActionRegistration {
	return nil
}

// Wait 等待 counter 回包。
func (m *CounterModule) Wait(ctx context.Context) (pbmsg.MseCounterState, error) {
	select {
	case <-ctx.Done():
		return pbmsg.MseCounterState{}, ctx.Err()
	case <-m.done:
		m.mu.Lock()
		err := m.err
		m.mu.Unlock()
		if err != nil {
			return pbmsg.MseCounterState{}, err
		}
		rsp, ok := m.Result()
		if !ok {
			return pbmsg.MseCounterState{}, fmt.Errorf("counter robot result missing")
		}
		return rsp, nil
	}
}

// Result 返回已收到的 counter 回包快照。
func (m *CounterModule) Result() (pbmsg.MseCounterState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.got
}

func (m *CounterModule) authOK() bool {
	if m.opts.Auth == nil {
		return true
	}
	rsp, ok := m.opts.Auth.Result()
	return ok && rsp.GetRet() == auth.RetOK
}

func (m *CounterModule) markSent() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sent {
		return true
	}
	m.sent = true
	return false
}

func (m *CounterModule) complete(rsp pbmsg.MseCounterState, err error) {
	m.mu.Lock()
	m.err = err
	if err == nil {
		m.result = rsp
		m.got = true
	}
	m.mu.Unlock()
	m.once.Do(func() {
		close(m.done)
	})
}
