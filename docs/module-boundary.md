# 模块边界与 aifd-core 准入

## 当前结论

当前不创建 `aifd-core`。现有代码里还没有一个能力同时满足无领域语义、无运行时依赖、至少两个框架基础设施模块稳定复用、并且无法合理归属已有模块。

框架统一使用仓库根 Go module `github.com/li9chuan/aifirstdev`，用一个根标签发布整套版本。
本文的“模块”表示目录、包和职责边界，不表示独立 Go module。现有包名、import 路径、
依赖方向和显式组合方式保持不变；`aifd-tools` 和官方插件也随框架一起发布。

只有 `aifd-examples/minigame` 和 `aifd-examples/desktool` 保留独立 `go.mod`，用于消费验证和
隔离示例构建环境，不单独发布模块标签。未来出现真实独立消费者、发布周期或依赖隔离需求时，
再评估拆分 Go module；不能仅凭职责不同或代码可复用就提前拆分。

## 根模块分类

### `aifd-contracts`

承载跨模块最小契约，例如 `ModuleDescriptor`、`Capability`、模块类型和极小日志接口。它只放多个基础设施模块都需要理解的稳定公共语言，不放具体 provider、业务状态、运行时流程或 feature 语义。

### 基础设施根模块

`aifd-auth`、`aifd-account`、`aifd-client`、`aifd-config`、`aifd-llm`、`aifd-observe`、`aifd-proxy`、`aifd-robot`、`aifd-rpc`、`aifd-rdb`、`aifd-runtime` 和 `aifd-schema` 按职责组织，在同一个 Go module 中统一版本：

- `aifd-auth`：账号中心和 proxy 共享的签名 ticket 基础件。
- `aifd-account`：可选账号中心实现，不强迫已有账号中心的业务项目依赖。
- `aifd-client`：Go 客户端请求、回包和推送，以及 proxy WebSocket transport。
- `aifd-config`：typed config bundle 生命周期、reload、manifest diff、validator 和 Luban bundle builder adapter。
- `aifd-llm`：provider-neutral 的 LLM 调用门面、stream、usage、tool call、JSON 输出、错误语义和 provider adapter。
- `aifd-observe`：日志、指标、trace、audit、stat sink。
- `aifd-proxy`：客户端连接、gateway、session、delivery 和登录接入边界；可选
  `adapters/nats/broker` 只作为 proxy-broker 入口显式封装 embedded NATS server 生命周期。
- `aifd-robot`：集成测试机器人、行为树、scenario、run group 和 report。
- `aifd-rpc`：message、session、route、dispatcher 和 transport adapter。
- `aifd-rdb`：saver、store、codec、Redis/memory 持久化抽象。
- `aifd-runtime`：service/user 生命周期、User、module 编排和恢复 gate。
- `aifd-schema`：proto/message 描述、兼容性检查和 RPC 绑定。

这些模块只放抽象、契约、适配和通用流程；业务 feature、业务 proto、真实配置表、业务 prompt、provider key、额度系统和审计落库留在业务项目或业务服务中。

## 构造边界和全局门面

框架默认使用显式构造，不引入 `wire`、`dig` 或运行时 DI container。接口依赖按值传，例如 `rpc.Bus`、`observe.Sink`、`logging.Logger`；有生命周期的具体对象按指针传，例如 `*rdb.DB`、`*rpc.Registry`、`*user.Runtime[T]`。

logging 是少数允许全局门面的横切能力。业务和框架代码可以使用 `log.Info`、`log.Warn`、`log.Error`、`log.Stat` 和 `log.Statf`，但这个门面只归属 `aifd-observe/logging`。不要新增 `share.Log`、`share.Stat` 或通用全局依赖中心。

`DB`、`Bus` 和 `Config` 不跟随 logging 放宽：DB 和 Bus 由 app/service host 创建、启动和关闭，再显式传给模块；Config 由 host 管理 reload，module 只接收窄配置接口或 typed config 值。

observe-agent 是 `aifd-observe` 的可选 control-plane 组件，不是业务插件。嵌入业务进程时，它只通过 admin RPC 按需响应 `tail/stats/health/flush` 查询，不启动后台扫描循环，不保存内存日志 ring buffer，也不参与业务热路径。运行日志本地优先写 stdout/file；observe 查询端需要查看时通过 RPC 拉取各进程 agent，由 agent 按逻辑 file 名 tail/filter 预注册的本地日志或 stat 文件。高频日志不通过业务 RPC 主动推送。嵌入式 agent 查询必须有 timeout、max bytes、max lines 和 max concurrency；初始化失败默认不阻断业务启动，除非宿主项目显式配置 `RequireObserveAgent=true`。

proxy-broker 是生产拓扑选择，不是业务服务能力。需要把 NATS 部署环节合并进进程时，由
proxy 入口显式启动 `aifd-proxy/adapters/nats/broker`，proxy 自身 RPC bus 可使用
in-process NATS 连接；game、world 或其他业务服只作为 NATS client 接入同一个 broker
集群，不隐式启动 broker，也不拥有 broker shutdown 顺序。

### `aifd-plugins/*`

只放官方可选业务能力插件，例如 mail、friend、guild、ranking。插件由应用入口显式组合进 `aifd-runtime`，import 不能自动注册 route、启动 worker 或创建运行期连接。

SDK facade、小工具包、runtime helper 和 provider client 不放进 `aifd-plugins/*`。例如 LLM client 保持在 `aifd-llm`；未来如果需要集中式 LLM 网关服务，再单独做业务 service module 或 `aifd-plugins/llm-gateway` 组合 `aifd-llm`。

### `aifd-tools`、`aifd-rules`、`aifd-examples`

- `aifd-tools`：CLI、更新检查、proto/config 命令入口、本地开发工具链安装与诊断。
- `aifd-rules`：AI 规则、skills、模板和 checklist。
- `aifd-examples`：业务形态示例，不作为生产运行时基础依赖。

示例里的 game、player、world、workspace、document 等词汇不能反向提升为框架公共概念。

## aifd-core 准入门槛

只有一个小能力同时满足以下条件，才允许新建未来的 `aifd-core/<pkg>`：

- 无业务领域语义，也无 provider、账号、连接、持久化、schema、runtime 等明确模块归属。
- 不能合理放入现有基础设施模块。
- 不依赖 `runtime`、`proxy`、`account`、`rpc`、`rdb` 等运行时对象。
- 至少两个基础设施根模块的生产代码稳定复用；测试、示例或临时代码复用不算。
- API 足够窄，包名能准确表达职责。

允许的未来包名应类似 `aifd-core/ticket` 或 `aifd-core/clock`。禁止新增 `aifd-share`、`aifd-common`、`utils`、`misc`，也禁止在 `aifd-core` 下创建泛化的 `common`、`helper`、`kit` 或 `share` 包。

## 当前候选判断

- `Clock`：暂不收。局部模块需要可测试时间时，用本模块 `ClockFunc` 或 `Now func()`。
- `Clone`：暂不收。config、rpc、schema、observe 的 clone 都是领域对象拷贝，不应抽成泛型 deep-copy。
- `ErrorKind`：暂不收。llm、rdb、auth、proxy 的错误语义不同，统一后容易变成空泛错误框架。
- `Retry/Backoff`：暂不收。当前主要绑定 rdb final-intent 持久化语义，不是通用 retry。
- `SSEReader`：暂不收。目前只被 `aifd-llm` 使用；等 observe、proxy 或 tools 也稳定使用 SSE 后再评估。
- `Func adapter`：暂不收。`SinkFunc`、`ValidatorFunc`、`ActionFunc` 这类本地小适配是 Go 惯例，集中会增加依赖。

## 新公共能力判断流程

1. 先判断能否归属现有模块；有明确领域归属时留在领域模块。
2. 如果多个模块需要它，优先让拥有语义的模块提供窄接口，或由业务项目在入口组合。
3. 如果仍无归属，记录至少两个基础设施模块的生产代码复用证据。
4. 满足准入门槛后，再提出 `aifd-core/<pkg>`，并用窄包名和窄 API 落地。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [按需求选择修改模块](../aifd-rules/skills/architecture-and-surface-routing/SKILL.md)
- [项目与 Feature 代码评审](../aifd-rules/skills/runtime-feature-review/SKILL.md)
