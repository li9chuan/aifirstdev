---
name: architecture-and-surface-routing
description: 判断需求在 aiFirstDev 中应落到哪个模块或项目面时使用。适用于需求涉及 contracts、schema、config、rpc、rdb、runtime、proxy、observe、robot、tools、rules、examples、可选功能插件或业务项目之间边界不清的情况。
---

# 架构与修改面路由

这个 skill 只负责判定 primary surface 和 secondary surfaces，不负责具体实现。

## 工作流

1. 先用一句话复述目标。
2. 判断主修改面，也就是拥有行为或真相的最小模块。
3. 列出必要联动面，只包含直接受影响的模块。
4. 为主修改面选择后续 skill。
5. 明确不应修改的模块，防止越层。

模块分类和未来 `aifd-core` 准入的当前决策源是工作区 [模块边界](../../../docs/module-boundary.md)。

## 路由表

- 最小公共类型、模块描述、服务角色：`aifd-contracts`。
- proto 描述、消息兼容、默认框架 PB、生成检查：`aifd-schema`，使用 [schema-proto-workflow](../schema-proto-workflow/SKILL.md)。
- Luban/config typed bundle 生命周期、热更、manifest diff、validator：`aifd-config`，使用 [config-table-workflow](../config-table-workflow/SKILL.md)。
- LLM provider-neutral 调用门面、stream、usage、tool call、JSON 输出和 provider adapter：`aifd-llm`。
- RPC message、session、route、dispatcher、adapter：`aifd-rpc`。
- saver、store、codec、Redis/memory 持久化抽象：`aifd-rdb`。
- service/root runtime、Root、module/lifecycle：`aifd-runtime`，使用 [runtime-module-development](../runtime-module-development/SKILL.md)。
- 客户端连接、gateway、session、delivery，以及显式 proxy-broker embedded NATS
  生命周期 adapter：`aifd-proxy`。
- 日志门面、指标、trace、audit、stat sink、KindRouter 和 observe-agent：`aifd-observe`。
- 集成测试机器人、行为树、run group、report：`aifd-robot`，使用 [robot-module-development](../robot-module-development/SKILL.md)。
- CLI、update、proto/config 命令入口、本地开发工具链安装与诊断：`aifd-tools`，服务入口变更使用 [service-entrypoint-development](../service-entrypoint-development/SKILL.md)。
- AI 规则、技能、模板、检查清单：`aifd-rules`。
- 示例用法和业务形态接线：`aifd-examples`。其中 `minigame` 展示完整游戏后端框架链路，`desktool` 展示桌面工具形态。
- Wails 桌面壳、Go binding、React + Ant Design 页面、桌面登录流程、桌面整包更新流程：业务项目或 `aifd-examples/desktool`，使用 [desktop-app-development](../desktop-app-development/SKILL.md)；组件 API 查询叠加 [ant-design-ui-development](../ant-design-ui-development/SKILL.md)。
- 官方可选功能能力，例如 mail、friend、guild、ranking：优先落到 `aifd-plugins/<name>` 或业务项目插件包；runtime 只接收其实现的 `root.Module` 或 `service.Module` 接口。
- 具体业务 feature、业务 proto、业务配置表、业务生成物：业务项目。
- 业务 prompt、provider key、额度策略、审计落库和 prompt 管理平台：业务项目或单独业务服务，不进入 `aifd-llm`。

## 基础工具集归属判定

- 不要用 `share` / `common` 承载边界不清的小工具；先判断能力是否能归属现有模块。
- 当前不创建 `aifd-core`。模块混乱优先通过文档、规则和窄职责归属收敛，不通过合并 Go module 解决。
- 根模块分类固定为四类：`aifd-contracts` 最小公共契约；`aifd-*` 基础设施根模块；`aifd-plugins/*` 可选业务能力插件；`aifd-tools` / `aifd-rules` / `aifd-examples` 工具、规则和示例。
- `aifd-account` 是可选账号中心实现，拥有 provider 校验、账号绑定、账号状态和 token 签发流程；已有账号中心的业务项目不应被迫依赖它。业务 owner、realm、role 或角色列表归业务项目。
- `aifd-proxy` 拥有客户端连接、packet、session、delivery 和登录接入边界；它不拥有账号中心实现，也不应强迫 `aifd-account` 反向依赖 proxy。
- `aifd-proxy/adapters/nats/broker` 可以封装 embedded NATS server 生命周期，但只能由
  proxy 入口显式 wiring；game/world/其他业务服只作为 NATS client 接入，不隐式启动 broker。
- `aifd-auth` 当前先保持独立，承载账号中心和 proxy 共享的 signed login ticket 基础件；不要为了减少 module 数量把它误并入 `aifd-account` 或 `aifd-proxy`。
- Wails CLI 属于 `aifd-tools` 的按需开发工具，使用 `aifd tools install --tool wails`；它不进入默认工具安装集合。
- NSIS 是 Windows 安装器环境，首版只在桌面示例和文档中作为构建前置检查，不由 `aifd-tools` 自动安装。
- 未来只有出现更多同类基础件，并且这些能力无领域归属、无运行时依赖、至少被两个基础设施根模块的生产代码稳定复用、且不能合理归属现有模块时，才考虑收敛为 `aifd-core/<package>`，例如 `aifd-core/ticket`。
- 进入未来基础工具集的包必须有明确包名和窄职责；不允许新增 `aifd-share`、`aifd-common` 或泛化的杂物目录。
- 当前 `Clock`、`Clone`、`ErrorKind`、`Retry/Backoff`、`SSEReader` 和 `Func adapter` 都不收进 core；优先留在拥有语义的模块内。

## 常见争议判定

- 服务生命周期、root runtime、service/root module 编排、恢复 gate、active root 管理：主面是 `aifd-runtime`。
- Player/World 是游戏业务词汇，不是框架默认命名；非游戏项目优先使用 user、workspace、document、index、jobs 等真实领域名。
- mail/friend/guild/ranking 这类跨主体能力优先落到 `aifd-plugins/<name>` 或业务项目插件包，不在 `aifd-runtime/root` 或 `aifd-runtime/service` 下建功能目录；需要跨 root 真相源时，由插件自己的 service module 承载，是否随 all-in-one 启动由服务入口决策。
- LLM 客户端能力优先落到独立 `aifd-llm`，不放进 `aifd-runtime`、`aifd-core` 或 `aifd-plugins/llm`；只有出现集中式 LLM 网关服务时，才另建业务 service module 或 `aifd-plugins/llm-gateway` 组合 `aifd-llm`。
- 纯 saver、Set/HSet、Redis/memory adapter、编解码持久化抽象：主面是 `aifd-rdb`。
- 客户端连接、packet codec、登录状态、重复登录和上下行投递：主面是 `aifd-proxy`。
- proxy-broker 一体化：主面是 `aifd-proxy/adapters/nats/broker` 和具体服务入口；业务服
  不拥有 broker 生命周期。
- robot scenario、client transport、AgentModule、行为树和报告：主面是 `aifd-robot`。
- 配置 typed bundle、reload、manifest diff、validator、Luban bundle builder adapter：主面是 `aifd-config`。
- `log.Info/Warn/Error`、`log.Stat/Statf`、observe sink、agent 本地日志 tail/filter 和观测查询 adapter：主面是 `aifd-observe`；不要新增 `share.Log`、`share.Stat` 或把高频日志通过业务 RPC 主动推送。嵌入式 observe-agent 是 control plane，只查询预注册文件，必须限制 timeout、读取量、行数和查询并发，不参与业务热路径。
- 具体 `DBPlayer` 字段、业务协议名、业务配置表和玩法状态迁移：主面是业务项目。
- Host placement、部署 profile、进程启动拓扑和本地 all-in-one/split 选择：主面是业务项目入口或 `aifd-tools` 示例接线；`aifd-runtime` 只候选 descriptor 校验、依赖检查、route ownership 和 ready gate，不做 `WorldRouter`、框架级 placement 表或部署 DSL。
- 业务 proto、route 常量和 generated route registry：主面是业务项目；`aifd-schema`/`aifd-rpc` 只做 descriptor 校验、route/schema 绑定检查和 typed dispatcher helper，不 import 业务 generated pb，也不保存业务 route 真相。
- observe/admin 的 bounded query envelope、probe 注册、tail、stats 和 health：主面是 `aifd-observe`；业务任务状态、业务计数解释等 payload 属于业务项目 probe，不进入统一业务 admin state。
- 业务 robot action 和 scenario 语义：主面是业务项目；`aifd-robot` 只拥有 runner、transport、callback、behavior、report 和 scenario 编排，不内置 scene/entity/move/AOI 等动作名。
- 示例项目按各自目标展示组合方式；`minigame` 可覆盖完整游戏后端框架链路，示例里的业务名不能反向进入框架模块。
- `service` 是逻辑生命周期编排，不等于必须引入微服务编排；同进程 all-in-one、本机多进程和 RPC/NATS/Redis 拆分都是部署选项。
- Ant Design MCP、CLI、LLMs.txt 和 X Skills 是开发期 AI 辅助体系；普通桌面应用运行时保持固定 React 组件和普通业务数据。只有用户明确要求“运行时 AI 助手动态 UI”时，才单独讨论 A2UI/XCard 类方案。
- Wails 桌面应用中的 WS/PB/client/cache/analyzer 和更新下载/校验/安装启动默认放 Go binding 后端；React 只做 UI 展示和轻量交互态。

## 边界原则

- 依赖方向保持单向：contracts/llm -> schema/config/rpc/rdb/proxy/observe/robot -> runtime -> business project。
- 基础设施模块不得 import runtime 或业务项目。
- 业务项目可以组合框架模块，但不应 import 框架 internal 包。
- 示例不是生产 feature；`minigame` 展示框架链路而不是完整玩法内容，`desktool` 展示桌面工具形态，不要把示例里的业务名提升为框架概念。

## 相关文档

- [skills 索引](../README.md)
- [模块边界与职责依据](../../../docs/module-boundary.md)
- [框架模块与用法入口](../../../README.zh-CN.md)
