package robot

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	robotc "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

// AuthFunc returns the client auth packet for one robot owner.
type AuthFunc func(ctx context.Context, uid int64) (*pbmsg.MceAuth, error)

// ModuleOptions 配置 minigame 登录 robot 模块。
type ModuleOptions struct {
	Auth AuthFunc
}

// AuthModule 是 minigame 登录流程的 robot 侧业务模块。
type AuthModule struct {
	opts ModuleOptions

	mu            sync.Mutex
	done          chan struct{}
	once          sync.Once
	result        pbmsg.MseAuth
	got           bool
	nextHeartbeat time.Time
}

// NewModule 创建登录 robot 模块。
func NewModule(opts ModuleOptions) *AuthModule {
	return &AuthModule{opts: opts, done: make(chan struct{})}
}

// Name 返回模块名。
func (m *AuthModule) Name() string {
	return "auth"
}

// OnAttach 注册登录回包 handler。
func (m *AuthModule) OnAttach(_ context.Context, agent robotc.Agent) error {
	return agent.On(m.handleAuth)
}

// OnLogin 发送客户端登录协议。
func (m *AuthModule) OnLogin(ctx context.Context, agent robotc.Agent) error {
	if m.opts.Auth == nil {
		return errors.New("auth robot issuer is nil")
	}
	req, err := m.opts.Auth(ctx, agent.UID())
	if err != nil {
		return err
	}
	return agent.Call(req)
}

// Tick renews the authenticated connection through the business owner.
func (m *AuthModule) Tick(ctx context.Context, agent robotc.Agent) error {
	m.mu.Lock()
	if !m.got || m.result.GetRet() != auth.RetOK || time.Now().Before(m.nextHeartbeat) {
		m.mu.Unlock()
		return nil
	}
	m.nextHeartbeat = time.Now().Add(30 * time.Second)
	m.mu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	if err := agent.Call(&proxypb.MceSessionHeartbeat{}, func(rsp *proxypb.MseSessionHeartbeat, err error) {
		if err == nil && (rsp == nil || rsp.GetServerTimeUnixMs() <= 0) {
			err = errors.New("invalid heartbeat response")
		}
		done <- err
	}); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-probeCtx.Done():
		return probeCtx.Err()
	}
}

// Actions 登录示例不注册行为树 action。
func (m *AuthModule) Actions() []robotc.ActionRegistration {
	return nil
}

// Wait 等待登录回包。
func (m *AuthModule) Wait(ctx context.Context) (pbmsg.MseAuth, error) {
	select {
	case <-ctx.Done():
		return pbmsg.MseAuth{}, ctx.Err()
	case <-m.done:
		rsp, ok := m.Result()
		if !ok {
			return pbmsg.MseAuth{}, errors.New("auth robot result missing")
		}
		return rsp, nil
	}
}

// Result 返回已收到的登录回包快照。
func (m *AuthModule) Result() (pbmsg.MseAuth, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.got
}

func (m *AuthModule) handleAuth(rsp *pbmsg.MseAuth) {
	m.mu.Lock()
	m.result = *rsp
	m.got = true
	m.mu.Unlock()
	m.once.Do(func() {
		close(m.done)
	})
}
