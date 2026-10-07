package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	client "github.com/li9chuan/aifirstdev/aifd-client"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	"github.com/li9chuan/aifirstdev/aifd-robot/behavior"
	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrAgentAwaitTimeout 表示 robot agent 等待回包超时。
	ErrAgentAwaitTimeout = client.ErrAwaitTimeout
	// ErrAgentClosed 表示通过已关闭的 robot agent 发送消息。
	ErrAgentClosed = client.ErrClosed
	// ErrAgentNotStarted 表示 await 调用发生在 agent 启动前。
	ErrAgentNotStarted = client.ErrNotStarted
)

// Agent 运行一个模拟客户端账号。
type Agent struct {
	id        string
	uid       int64
	account   string
	client    *client.Client
	modules   []robot.AgentModule
	tree      *behavior.Tree
	report    *robot.Report
	observe   observe.Sink
	tickEvery time.Duration
	loggedIn  atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// AgentOptions 配置单个 agent。
type AgentOptions struct {
	ID              string
	UID             int64
	Account         string
	Transport       robot.Transport
	Modules         []robot.AgentModule
	Behavior        *behavior.Tree
	Report          *robot.Report
	Observe         observe.Sink
	TickInterval    time.Duration
	CallbackTimeout time.Duration
}

// NewAgent 校验选项并创建一个模拟客户端 agent。
func NewAgent(opts AgentOptions) (*Agent, error) {
	if opts.ID == "" {
		return nil, fmt.Errorf("robot agent id is empty")
	}
	if opts.Transport == nil {
		return nil, fmt.Errorf("robot agent %s transport is nil", opts.ID)
	}
	if opts.Report == nil {
		opts.Report = &robot.Report{}
	}
	if opts.TickInterval <= 0 {
		opts.TickInterval = time.Second
	}
	if opts.CallbackTimeout <= 0 {
		opts.CallbackTimeout = 15 * time.Second
	}
	rpcClient, err := client.New(opts.Transport, client.Options{
		AwaitTimeout: opts.CallbackTimeout,
		Observe:      opts.Observe,
	})
	if err != nil {
		return nil, err
	}
	return &Agent{
		id:        opts.ID,
		uid:       opts.UID,
		account:   opts.Account,
		client:    rpcClient,
		modules:   append([]robot.AgentModule(nil), opts.Modules...),
		tree:      opts.Behavior,
		report:    opts.Report,
		observe:   opts.Observe,
		tickEvery: opts.TickInterval,
		done:      make(chan struct{}),
	}, nil
}

// ID 返回 scenario 内稳定的 agent ID。
func (a *Agent) ID() string {
	return a.id
}

// UID 返回分配给该 agent 的用户 ID。
func (a *Agent) UID() int64 {
	return a.uid
}

// Account 返回分配给该 agent 的账号名。
func (a *Agent) Account() string {
	return a.account
}

// IsLoggedIn 表示该 agent 是否已完成 Login。
func (a *Agent) IsLoggedIn() bool {
	return a.loggedIn.Load()
}

// Call 通过通用客户端门面发送 protobuf 请求或异步等待回包。
func (a *Agent) Call(req proto.Message, args ...any) error {
	return a.client.Call(req, args...)
}

// On 注册无 client inbox 的服务端 protobuf push handler。
func (a *Agent) On(handler any) error {
	return a.client.On(handler)
}

// Attach 注册模块消息 handler 和行为 action。
func (a *Agent) Attach(ctx context.Context) error {
	for _, module := range a.modules {
		if err := module.OnAttach(ctx, a); err != nil {
			return fmt.Errorf("robot module %s attach: %w", module.Name(), err)
		}
	}
	return nil
}

// Login 将 agent 标记为在线，并调用模块登录 hook。
func (a *Agent) Login(ctx context.Context) error {
	if a.loggedIn.Swap(true) {
		return nil
	}
	a.report.AddLogin(1)
	for _, module := range a.modules {
		if err := module.OnLogin(ctx, a); err != nil {
			a.report.AddError(1)
			return fmt.Errorf("robot module %s login: %w", module.Name(), err)
		}
	}
	a.writeObserve(ctx, "robot_login", 1, map[string]string{"agent": a.id})
	return nil
}

// Start 启动该 agent 的客户端读取循环和 tick 循环。
func (a *Agent) Start(ctx context.Context) error {
	if err := a.client.Start(); err != nil {
		return err
	}
	a.wg.Add(1)
	go a.tickLoop(ctx)
	return nil
}

// Close 停止循环，并只关闭一次底层 transport。
func (a *Agent) Close(ctx context.Context) error {
	var err error
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		close(a.done)
		err = a.client.Close()
		a.wg.Wait()
	})
	return err
}

func (a *Agent) tickLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(a.tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.done:
			return
		case <-ticker.C:
			if a.closed.Load() {
				return
			}
			if !a.loggedIn.Load() {
				continue
			}
			for _, module := range a.modules {
				if err := module.Tick(ctx, a); err != nil {
					a.report.AddError(1)
				}
			}
			if a.tree != nil {
				a.tree.Tick(ctx, a)
			}
		}
	}
}

func (a *Agent) writeObserve(ctx context.Context, name string, value float64, dimensions map[string]string) {
	if a.observe == nil {
		return
	}
	_ = a.observe.Write(ctx, []observe.Event{observe.Metric(name, value, dimensions)})
}
