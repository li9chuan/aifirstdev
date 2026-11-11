# aifd-observe

## 定位与边界

`aifd-observe` 提供中立观测事件、Sink、fan-out/异步投递 helper、memory/console/JSONL
adapter，以及可选的本地查询 agent。指标、trace、audit、健康信号及显式 `KindStat` 事件
可通过这些接口组合。

进程日志、typed Stat 的定义和 CSV 编码、本地滚动文件归 [aifd-log](../aifd-log/README.md)。
本模块不拥有日志全局门面，不要求业务启动 observe 服务，不内置聚合或补录服务；业务状态、
报表和平台消费语义留在项目。User 公共头接线归 runtime，见 [Stat](../docs/stat.md)。

## 可选日志接入

需要将普通日志转换为观测事件时，由宿主显式接入 adapter：

```go
import (
    "github.com/li9chuan/aifirstdev/aifd-log"
    loggingsink "github.com/li9chuan/aifirstdev/aifd-observe/adapters/logging"
)

closeLog, err := logging.InitDefault(logging.Config{
    SourceService: "game",
    LocalPath: "run/logs/game.log",
    SecondaryHandler: loggingsink.NewHandler(sink),
})
if err != nil { return err }
defer closeLog()
```

普通日志先写本地文本，成功后同步调用可选 handler，其错误不回退本地记录。慢出口可以
由宿主先组合 `AsyncSink`，并负责其 Start/Close。typed Stat 的 CSV 不经过这个 handler，
项目如需转发，应显式配置独立的 `StatConfig.Forward`。

旧字段式 `Stat/Statf` 与全局默认 Sink 已移除。需要观测 envelope 的调用方直接使用
`contracts.Event` 和 `Sink.Write`；这类事件与 typed CSV 不自动互转。agent 作为 Sink
只累计计数，不保存事件正文。所有高频普通日志仍本地优先，不通过业务 RPC 主动推送。

agent 的 tail 支持当前普通文本与历史 JSON。应用自定义 CSV 保留原文，不推断头字段或
事件 schema；不自动遍历归档文件或提供补录。日志级别、名字和时间筛选依赖可识别的普通
日志字段。

### Embedded observe-agent

observe-agent 可以嵌入业务进程，但它属于 control plane，不在业务热路径中。agent 本体不启动
后台扫描循环，不做 tail follow，也不保存内存 ring buffer；只有 observe 聚合端调用
`observe.tail`、`observe.stats`、`observe.health` 或 `observe.flush` 时才按需执行查询。

嵌入式 agent 的 tail/filter 查询必须配置保护边界：`QueryTimeout`、`MaxBytes`、`MaxLines`
和 `MaxConcurrency`。外部只注册可查询文件，agent 内部从文件路径推导目录边界；
`MaxConcurrency` 默认是 1；并发满时立即返回错误，不排队等待，避免 observe 查询反压
业务进程。`Stats` / `Health` 保持轻量查询，不参与高成本文件读取。

agent 初始化失败默认只应记录 warn 并让业务继续启动。只有宿主项目显式配置
`RequireObserveAgent=true` 时，才把 agent 初始化失败升级为服务启动失败；这个开关属于
宿主项目启动策略，不进入 `agent.Config`。

AsyncSink 需显式 `Start(ctx)`；队列满会丢弃并累计 `Dropped()`，底层写入错误不自动重试。
要排空队列时先停止生产者并调用 Close，再取消 worker context 和关闭底层 sink；先取消 context 不保证队列排空。
实现见 [日志门面](../aifd-log/README.md)、[AsyncSink](runtime/async.go) 和 [agent](agent/agent.go)。

新增功能文件不需要机械打日志；只在启动/恢复、RPC 入口关键失败、外部依赖失败、
异步 worker、跨期结算和关键状态迁移等边界写日志。日志字段必须能定位主体、周期、
动作和失败原因，不打印完整 proto、排行榜、背包或隐私字段。

## 更新边界

小版本可以增加事件字段或 adapter，但必须保持现有 sink 契约兼容。改变事件语义或
sink 行为时必须明确说明兼容性影响。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [项目级编码约束](../aifd-rules/skills/project-coding-constraints/SKILL.md)
- [服务入口开发](../aifd-rules/skills/service-entrypoint-development/SKILL.md)
