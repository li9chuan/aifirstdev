package behavior

import (
	"context"
	"fmt"
	"time"

	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

// Node 是行为树节点。
type Node interface {
	Tick(context.Context, robot.Agent) robot.Status
}

// ActionNode 将业务 action 包装为行为节点。
type ActionNode struct {
	Action robot.Action
}

// Tick 执行包装的 action，并将缺失 action 视为失败。
func (n ActionNode) Tick(ctx context.Context, agent robot.Agent) robot.Status {
	if n.Action == nil {
		return robot.StatusFailure
	}
	return n.Action.Tick(ctx, agent)
}

// Sequence 只有在所有子节点成功时才成功。
type Sequence struct {
	Children []Node
}

// Tick 按顺序执行子节点，直到某个子节点暂停或失败。
func (n *Sequence) Tick(ctx context.Context, agent robot.Agent) robot.Status {
	for _, child := range n.Children {
		status := child.Tick(ctx, agent)
		if status != robot.StatusSuccess {
			return status
		}
	}
	return robot.StatusSuccess
}

// Selector 在第一个子节点成功时成功。
type Selector struct {
	Children []Node
}

// Tick 按顺序执行子节点，直到某个子节点不再失败。
func (n *Selector) Tick(ctx context.Context, agent robot.Agent) robot.Status {
	for _, child := range n.Children {
		status := child.Tick(ctx, agent)
		if status != robot.StatusFailure {
			return status
		}
	}
	return robot.StatusFailure
}

// Repeat 多次执行子节点；Count <= 0 表示无限重复。
type Repeat struct {
	Child Node
	Count int
	done  int
}

// Tick 重复执行子节点，直到成功次数达到 Count；未设置 Count 时持续重复。
func (n *Repeat) Tick(ctx context.Context, agent robot.Agent) robot.Status {
	if n.Child == nil {
		return robot.StatusFailure
	}
	if n.Count > 0 && n.done >= n.Count {
		return robot.StatusSuccess
	}
	status := n.Child.Tick(ctx, agent)
	if status == robot.StatusSuccess {
		n.done++
	}
	if n.Count > 0 && n.done >= n.Count {
		return robot.StatusSuccess
	}
	return robot.StatusRunning
}

// Wait 在等待时间结束前返回 running。
type Wait struct {
	Duration time.Duration
	started  time.Time
}

// Tick 在配置的等待时间结束前返回 running。
func (n *Wait) Tick(context.Context, robot.Agent) robot.Status {
	if n.Duration <= 0 {
		return robot.StatusSuccess
	}
	if n.started.IsZero() {
		n.started = time.Now()
	}
	if time.Since(n.started) >= n.Duration {
		return robot.StatusSuccess
	}
	return robot.StatusRunning
}

// Tree 是行为树根节点。
type Tree struct {
	Root Node
}

// Tick 执行行为树根节点，并将空树视为成功。
func (t *Tree) Tick(ctx context.Context, agent robot.Agent) robot.Status {
	if t == nil || t.Root == nil {
		return robot.StatusSuccess
	}
	return t.Root.Tick(ctx, agent)
}

// Build 根据 scenario 行为描述创建行为节点树。
func Build(spec robot.BehaviorSpec, registry *Registry) (Node, error) {
	switch spec.Type {
	case "", "action":
		if registry == nil {
			return nil, fmt.Errorf("behavior action %s registry is nil", spec.Name)
		}
		action, err := registry.Build(spec.Name, spec.Settings)
		if err != nil {
			return nil, err
		}
		return ActionNode{Action: action}, nil
	case "sequence":
		children, err := buildChildren(spec.Children, registry)
		if err != nil {
			return nil, err
		}
		return &Sequence{Children: children}, nil
	case "selector":
		children, err := buildChildren(spec.Children, registry)
		if err != nil {
			return nil, err
		}
		return &Selector{Children: children}, nil
	case "repeat":
		children, err := buildChildren(spec.Children, registry)
		if err != nil {
			return nil, err
		}
		if len(children) != 1 {
			return nil, fmt.Errorf("repeat expects exactly one child, got %d", len(children))
		}
		return &Repeat{Child: children[0], Count: getIntSetting(spec.Settings, "count")}, nil
	case "wait":
		return &Wait{Duration: getDurationSetting(spec.Settings, "duration")}, nil
	default:
		return nil, fmt.Errorf("unknown behavior node type %s", spec.Type)
	}
}

func buildChildren(specs []robot.BehaviorSpec, registry *Registry) ([]Node, error) {
	children := make([]Node, 0, len(specs))
	for _, spec := range specs {
		child, err := Build(spec, registry)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}

func getIntSetting(settings robot.Settings, name string) int {
	if settings == nil {
		return 0
	}
	switch v := settings[name].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func getDurationSetting(settings robot.Settings, name string) time.Duration {
	if settings == nil {
		return 0
	}
	switch v := settings[name].(type) {
	case time.Duration:
		return v
	case int:
		return time.Duration(v)
	case int64:
		return time.Duration(v)
	case float64:
		return time.Duration(v)
	case string:
		d, _ := time.ParseDuration(v)
		return d
	default:
		return 0
	}
}
