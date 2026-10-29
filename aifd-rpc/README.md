# aifd-rpc

## 定位

`aifd-rpc` 提供传输中立的 RPC 契约、路由注册、分发、session、codec 和 adapter
边界。

## 负责

- 顶层 `rpc` 业务门面，默认只向业务暴露 `Session + PB` handler。
- `Message`、`Session`、`Handler`、`Route`、`Bus`、`Delivery`、stats 和 codec 契约。
- protobuf 消息的框架级 marshal / unmarshal helper。
- 进程内 router、delivery、runner、callback timeout 和统计行为。
- Local adapter，用于本进程 RPC、单测、runtime 回环和 mock。
- NATS adapter，用于 `RPC.` request/reply、`S.` client push 和 `js.` JetStream 事件。
- 旧 `binding/proto` 中保留的反射绑定兼容入口；新业务使用顶层 Session + PB 门面。

## 不负责

- Player、World、DBPlayer、持久化或玩法逻辑。
- 业务 `.proto` 定义或生成的 pb 包。
- Runtime lifecycle、feature module 归属或配置加载。
- 具体业务项目的共享常量、service 注册规则、奖励/邮件/玩法 stream 命名封装。

## 业务项目如何使用

业务项目默认只 import `github.com/li9chuan/aifirstdev/aifd-rpc`，并使用 `rpc` 注册 client、
server、admin 或 stream route。业务 handler 固定收敛为 `func(*rpc.Session)` 或
`func(*rpc.Session, *Req)`，其中 `*Req` 必须是
protobuf message。业务状态变更应发生在拥有职责的 runtime 或 feature 模块中。

业务 handler 不直接返回 pb 或 error，也不默认接收 `context.Context`、uid、`Message`、
`Header`、`Bus`。成功回包使用 `s.Response(pb)`，失败回包使用 `s.Error(code, message)`，
服务间调用通过 `s.Tell`、`s.Request`、`s.Forward` 表达。

`Response/Error` 只完成原调用的响应通道；没有 responder 时明确失败，不使用 UID
Push 兜底。`Forward` 保留原响应通道，异步取得被转发结果，避免同 UID runner 内
嵌套阻塞。`Push/PushTo` 发布独立用户事件，自动清除客户端 inbox。Local/NATS
保持相同语义。普通用户业务可使用 [UserRuntime](../aifd-runtime/README.md) 自动加载并
注入 User；非用户路由仍使用本模块入口。

Local Bus 通过 UID 订阅直接投递当前事件，不保存推送历史。测试使用 `SubscribePush`
观察投递，或使用 `runtime.NewSession` 的响应/推送 hook；消息断言留在测试文件内。

推荐用法：

- 业务 feature 的 `RegisterRPC` 接收 `*rpc.Registry`，由启动层统一 `rpc.Install` 到真实 bus。
- 本进程开发、单测和 runtime 回环使用 `adapters/local.NewBus`。
- 生产跨进程通信使用 `adapters/nats.NewBus`。
- 需要可靠事件时使用 `RegisterStream` / `SaveToStream`，不要把 stream 事件混进普通 RPC 命名。
- 框架启动层、proxy、robot、adapter 可使用 `contracts.Message`、`Bus`、`Route` 等底层接口。
- `rpcpb` 子包保留为 deprecated 兼容别名；新业务代码直接使用顶层 `rpc`。
- `binding/proto` 保留为迁移兼容辅助；新业务代码优先使用顶层 `rpc`。
- 业务 protobuf 不进入 `aifd-rpc`。

宿主先恢复业务模块并完成注册，再 `rpc.Start(ctx, bus, reg)` 安装并启动 runner，或显式 Install + Run。
Bus 仍由宿主构造和 Close。当前 `rpc.Start` 以 background context 启动 runner；显式 Install +
`rpc.Run(ctx, bus)` 可将宿主 context 传给 delivery/handler，但取消 context 不会停止 runner 的取消息循环。
两种接线都以 Bus 的 Deliveries channel 结束作为消费退出条件。`rpc.Start` 不暴露 runner 等待接口；
需要确认排空时，由宿主显式 Install + Run 并保留 Run 的完成信号，关闭 Bus、等待 Run 返回后再关闭 handler 依赖。
实现和完整签名见 [facade.go](facade.go)、[registry.go](registry.go) 和 [Session](session.go)。

默认 header 语义：

- `uid`：请求主体。
- `s`：客户端 inbox。
- `rsp`：服务端推送或回包的 topic / 消息名。
- `pbType`：payload 对应的 protobuf 结构名；topic 为空时可作为默认 topic。
- `Status`：transport 错误状态。
- `traceID`：链路追踪预留字段。

## 更新边界

RPC 升级应保持 `Message`、`Session`、`Route` 和 `Bus` 契约。新的 adapter 应保持
可选，不能强制业务项目 import 内部包。NATS subject 默认兼容 `RPC.`、`S.`、`js.`
约定，但必须允许业务项目通过 config 替换。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Schema 与 Proto 工作流](../aifd-rules/skills/schema-proto-workflow/SKILL.md)
- [服务入口开发](../aifd-rules/skills/service-entrypoint-development/SKILL.md)
