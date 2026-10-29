# aifd-robot

## 定位

`aifd-robot` 是面向 AI 优先开发的集成测试和模拟客户端框架，提供 robot agent、
run group、消息路由、callback 等待、行为执行和测试统计。

## 负责

- Agent、module、scenario、transport factory、behavior action 和 report 契约。
- Run group 调度、agent 生命周期、消息路由、callback 等待、tick loop 和 report 聚合。
- 最小行为树节点，以及 local adapter 形态。
- 复用 `aifd-client/adapters/proxyws` 连接真实客户端网关。

## 不负责

- 业务 robot manager、真实行为场景或 `.b3` 资产。
- Player、World、DBPlayer、玩法 feature、GM 命令或真实项目 proto 细节。
- Runtime 服务归属或业务压测目标。

## 业务项目如何使用

业务项目从 `features/<name>/robot` 或 `tests/robot` 注册 robot module 和 action，
再通过 local transport 跑 CI 场景，或通过 `aifd-client/adapters/proxyws` 跑真实
proxy 集成测试。

Robot 业务接口默认使用 protobuf，出站调用只使用 `Call`。`Call/On` 由通用
`aifd-client` 提供，wire topic 从 protobuf message 名称推导，robot 只负责
agent、module、scenario 和行为树生命周期：

- `agent.Call(req)`：只发送，不等待回包。
- `agent.Call(req, func(rsp *Rsp, err error) { ... })`：发送并异步等待回包，是 module/tick 里的默认 await 用法。

入站 push 使用 `On` 注册 typed handler：

```go
agent.On(func(msg *pbmsg.Push) {
	// update local robot state
})
```

await 超时缺省 15 秒，直接构造 Agent 时可用 `AgentOptions.CallbackTimeout` 设置；Call 不支持单次覆盖。底层
`rpc.Message`、`HeaderClientInbox` 和 callback cache 都属于 runtime/transport 内部细节，
业务模块不要直接访问。

组运行入口是 `runtime.NewRunGroup(RunGroupOptions{Scenario, Transport, Modules, ...})`，
显式 `Start(ctx)` 后由宿主管理运行时间并 Close，最后读取 `Report()`。
Scenario 保存人数/账号范围/行为和时间，传输工厂在 RunGroupOptions，不是 Scenario 字段。
Report 当前只有 Login、Send、Receive、Error 和 Custom 计数，没有内置延迟直方图。

`adapters/b3` 接受外部 Loader 输出的中立 Project 并构建独立行为树，不直接解析 `.b3` 文件。
实际接线见 [minigame robot](../aifd-examples/minigame/cmd/robot/main.go)，类型见 [RunGroup](runtime/rungroup.go) 和 [Report](contracts/report.go)。

## 更新边界

Robot 升级应保持 agent、module、scenario 和 transport 契约。新的行为 adapter 不应
把具体业务 action 名称引入框架。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Robot 模块开发](../aifd-rules/skills/robot-module-development/SKILL.md)
- [Minigame 示例](../aifd-examples/minigame/README.md)
