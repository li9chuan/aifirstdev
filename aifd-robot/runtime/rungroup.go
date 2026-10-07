package runtime

import (
	"context"
	"fmt"
	"strconv"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	"github.com/li9chuan/aifirstdev/aifd-robot/behavior"
	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

// TransportFactory 为每个 agent 创建一个 transport。
type TransportFactory func(robot.AgentSpec) (robot.Transport, error)

// ModuleFactory 创建 agent 独有模块。
type ModuleFactory func(robot.AgentSpec) []robot.AgentModule

// RunGroup 为一个 scenario 启动一组模拟客户端。
type RunGroup struct {
	scenario  robot.Scenario
	transport TransportFactory
	modules   ModuleFactory
	observe   observe.Sink
	report    *robot.Report
	agents    []*Agent
}

// RunGroupOptions 配置 run group。
type RunGroupOptions struct {
	Scenario         robot.Scenario
	Transport        TransportFactory
	Modules          ModuleFactory
	BehaviorRegistry *behavior.Registry
	Observe          observe.Sink
}

// NewRunGroup 为 scenario 构建全部 agent、transport、模块和行为树。
func NewRunGroup(opts RunGroupOptions) (*RunGroup, error) {
	if opts.Scenario.RunNum <= 0 {
		return nil, fmt.Errorf("robot scenario run num must be positive")
	}
	if opts.Transport == nil {
		return nil, fmt.Errorf("robot transport factory is nil")
	}
	report := &robot.Report{}
	registry := opts.BehaviorRegistry
	if registry == nil {
		registry = behavior.NewRegistry()
	}
	registeredActions := make(map[string]struct{})
	group := &RunGroup{
		scenario:  normalizeScenario(opts.Scenario),
		transport: opts.Transport,
		modules:   opts.Modules,
		observe:   opts.Observe,
		report:    report,
	}
	for i := 0; i < group.scenario.RunNum; i++ {
		spec := group.makeAgentSpec(i)
		transport, err := opts.Transport(spec)
		if err != nil {
			return nil, err
		}
		modules := []robot.AgentModule(nil)
		if opts.Modules != nil {
			modules = opts.Modules(spec)
		}
		if err := registerModuleActions(registry, registeredActions, modules); err != nil {
			return nil, err
		}
		var tree *behavior.Tree
		if group.scenario.Behavior.Type != "" || group.scenario.Behavior.Name != "" {
			root, err := behavior.Build(group.scenario.Behavior, registry)
			if err != nil {
				return nil, err
			}
			tree = &behavior.Tree{Root: root}
		}
		agent, err := NewAgent(AgentOptions{
			ID:           spec.ID,
			UID:          spec.UID,
			Account:      spec.Account,
			Transport:    transport,
			Modules:      modules,
			Behavior:     tree,
			Report:       report,
			Observe:      opts.Observe,
			TickInterval: group.scenario.TickInterval,
		})
		if err != nil {
			return nil, err
		}
		group.agents = append(group.agents, agent)
	}
	return group, nil
}

func registerModuleActions(registry *behavior.Registry, registered map[string]struct{}, modules []robot.AgentModule) error {
	for _, module := range modules {
		for _, action := range module.Actions() {
			if _, ok := registered[action.Name]; ok {
				continue
			}
			if err := registry.Register(action); err != nil {
				return err
			}
			registered[action.Name] = struct{}{}
		}
	}
	return nil
}

// Start 对组内每个 agent 执行 attach、start 和 login。
func (g *RunGroup) Start(ctx context.Context) error {
	for _, agent := range g.agents {
		if err := agent.Attach(ctx); err != nil {
			return err
		}
		if err := agent.Start(ctx); err != nil {
			return err
		}
		if err := agent.Login(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭全部 agent，并返回第一个关闭错误。
func (g *RunGroup) Close(ctx context.Context) error {
	var firstErr error
	for _, agent := range g.agents {
		if err := agent.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Agents 返回组内 agent 指针快照。
func (g *RunGroup) Agents() []*Agent {
	out := make([]*Agent, len(g.agents))
	copy(out, g.agents)
	return out
}

// Report 返回共享 run group 计数快照。
func (g *RunGroup) Report() robot.ReportSnapshot {
	report := g.report.Snapshot()
	for _, agent := range g.agents {
		stats := agent.client.Stats()
		report.Send += stats.Send
		report.Receive += stats.Receive
		report.Error += stats.Error
	}
	return report
}

func (g *RunGroup) makeAgentSpec(index int) robot.AgentSpec {
	uid := g.scenario.UIDStart + int64(index)
	accountNo := g.scenario.AccountStart + int64(index)
	return robot.AgentSpec{
		ID:      g.scenario.Name + "-" + strconv.Itoa(index),
		UID:     uid,
		Account: g.scenario.AccountPrefix + strconv.FormatInt(accountNo, 10),
		Index:   index,
	}
}

func normalizeScenario(s robot.Scenario) robot.Scenario {
	if s.Name == "" {
		s.Name = "robot"
	}
	if s.UIDStart <= 0 {
		s.UIDStart = 1
	}
	if s.AccountPrefix == "" {
		s.AccountPrefix = "robot"
	}
	if s.TickInterval <= 0 {
		s.TickInterval = time.Second
	}
	return s
}
