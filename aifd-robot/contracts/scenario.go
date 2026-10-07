package contracts

import "time"

// Scenario 描述 robot run group；业务项目可以从 YAML/TOML/JSON 加载，但框架 runtime 表示不绑定格式。
type Scenario struct {
	Name          string
	RunNum        int
	UIDStart      int64
	AccountStart  int64
	AccountPrefix string
	TickInterval  time.Duration
	Duration      time.Duration
	Behavior      BehaviorSpec
}

// BehaviorSpec 是格式中立的行为树描述。
type BehaviorSpec struct {
	Type     string
	Name     string
	Settings Settings
	Children []BehaviorSpec
}

// AgentSpec 是从 scenario 创建的单个具体 robot。
type AgentSpec struct {
	ID      string
	UID     int64
	Account string
	Index   int
}
