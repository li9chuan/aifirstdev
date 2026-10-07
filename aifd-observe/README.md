# aifd-observe

## 定位

`aifd-observe` 提供框架级可观测事件和 sink，用于日志、指标、链路、审计、统计和
健康信号。

## 负责

- 中立的可观测事件 envelope。
- Sink 接口，以及 fan-out 或异步投递 helper。
- 用于测试和示例的本地 memory、console、stat adapter。
- local-first 日志门面：业务通过 AIFD logger 写日志，本地文件是必达路径，observe 是可选转发。
- 业务打点门面：`log.Stat` / `log.Statf` 生成 `KindStat` 事件，由 sink 决定输出到本地文件、
  observe-agent 或外部打点日志服务器。
- 可选 observe-agent：按预注册文件 tail/filter 本地已输出日志，供 observe 聚合端通过 RPC 查询。

## 不负责

- 玩法聚合真相、排行榜、奖励结算或业务报表。
- Player、World、DBPlayer、feature 状态或 runtime lifecycle 决策。
- 超出框架事件形态之外的具体业务日志策略。

## 业务项目如何使用

业务项目可以通过此模块写入指标、日志和统计，也可以接入自己的 sink 实现。feature
代码应把业务真相留在拥有职责的 feature 模块中，只把观测信号发布到这里。

日志使用 `logging` 包初始化进程级默认 logger：

```go
closeLog, err := logging.InitDefault(logging.Config{
	SourceService: "game",
	LocalPath:     "run/logs/game.log",
	AlsoStdout:    true,
})
if err != nil {
	return err
}
defer closeLog()

logging.Info("service started", logging.String("module", "game"))
logging.InfoContext(ctx, "rpc handled", logging.String("topic", topic))
```

业务打点也走 `logging` 包的全局门面，但它不是普通运行日志：

```go
logging.Stat("friend.add", logging.Int64("uid", uid), logging.Int64("friend_uid", friendUID))
logging.Statf("type=%s uid=%d", typ, uid)
```

`Info` / `Warn` / `Error` 的首要目标是本地 stdout/file。observe 查询日志时，聚合端通过
RPC 调用各进程 observe-agent，agent 只按逻辑 file 名查询预注册日志或 stat 文件。
高频运行日志不通过 RPC 主动推送。

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

`Stat` / `Statf` 生成 `KindStat` 事件。入口层可以使用 `runtime.KindRouter`、
`runtime.MultiSink` 和 `runtime.AsyncSink` 将打点路由到本地 JSONL、observe-agent、
外部打点日志服务器或测试 recorder。业务代码不判断输出目标。

新增功能文件不需要机械打日志；只在启动/恢复、RPC 入口关键失败、外部依赖失败、
异步 worker、跨期结算和关键状态迁移等边界写日志。日志字段必须能定位主体、周期、
动作和失败原因，不打印完整 proto、排行榜、背包或隐私字段。

## 更新边界

小版本可以增加事件字段或 adapter，但必须保持现有 sink 契约兼容。改变事件语义或
sink 行为时必须明确说明兼容性影响。
