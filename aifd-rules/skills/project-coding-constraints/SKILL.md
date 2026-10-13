---
name: project-coding-constraints
description: 编码、整理、重构、命名调整、问题分析或 review 后修代码时使用。适用于 aiFirstDev 项目的职责边界、最小业务门面、框架复杂度下沉、service/user 命名、可选功能插件、公开 API 收窄、文件组织、锁与拷贝、持久化、注释、日志、错误、测试边界和项目经验沉淀。
---

# 项目级编码约束

这是 aiFirstDev 的通用编码约束层。进入代码编辑、重排、命名调整、问题分析或 review 后修代码时，先使用它，再叠加具体模块 skill。

## 职责边界

- 先选择最小职责归属模块：`contracts`、`llm`、`schema`、`config`、`rpc`、`rdb`、`proxy`、`observe`、`robot`、`runtime`、`tools`、`rules`、`examples` 或业务项目。
- 基础设施模块只放抽象、契约、适配和通用流程；业务 feature 真相、业务 proto、业务配置、生成物和玩法规则留在业务项目。
- 可选框架能力包必须由应用入口显式组合，import 必须无副作用；不能用隐藏全局注册绕过启动 wiring，也不要在 `aifd-runtime/user` 或 `aifd-runtime/service` 下为 mail、friend、guild、ranking 这类功能建子目录。
- proxy-broker embedded NATS 属于 `aifd-proxy/adapters/nats/broker` 的显式入口能力；业务服
  只连接 broker 集群，不拥有或隐式启动 broker 生命周期。
- 不把业务逻辑堆到 RPC handler、proxy route、工具命令、入口 wiring 或临时胶水层；handler 应调用拥有状态的 module。
- 改动保持小而完整，只补直接必要联动，不顺手重构无关区域，不统一格式化无关文件。
- 职责目录共享根 Go module 和整套版本；仅两个示例保留独立 `go.mod`。独立消费、发布周期或依赖隔离需求有实际证据后，才评估拆分 Go module。
- 通用框架不保留外部试验项目的专名、内部路径、业务参数、专属操作规程或评估记录；相关经验只能抽象成通用规则。第三方依赖名称、许可证归属和正常技术术语应如实保留。

## 基础工具集边界

- 模块分类和未来 `aifd-core` 准入以工作区 [模块边界](../../../docs/module-boundary.md) 为准。
- 禁止重建旧式 `share` 垃圾桶。不要新增 `share`、`common`、`misc`、`utils` 这类无职责边界的公共目录或 module。
- 新增小工具前先搜索现有模块和调用点，优先归属拥有使用边界的模块：连接与登录接入归 `proxy`，RPC helper 归 `rpc`，持久化 helper 归 `rdb`，schema helper 归 `schema`，账号中心实现归 `account`。
- 如果能力同时被多个模块需要，但仍有明确领域归属，应保持在领域模块内，由其他模块通过窄接口或业务项目组合使用；不要为了减少 import 数量移动到公共工具集。
- 只有同时满足这些条件，才允许进入未来基础工具集：无业务领域语义、无法归属现有模块、不依赖 runtime/proxy/account/rpc/rdb 的运行时对象、至少两个框架模块稳定复用、有明确包名和窄 API。
- `aifd-auth` 这类 signed login ticket 基础件当前保持独立；在没有更多同类基础件前，不创建 `aifd-core`，也不把它并入可选账号中心 `aifd-account`。
- 当前 `Clock`、`Clone`、`ErrorKind`、`Retry/Backoff`、`SSEReader` 和 `Func adapter` 都不收进 core；优先留在本模块或拥有语义的模块内。

## 业务门面

- 面向业务的基础设施模块优先提供最小业务门面，接口表达业务动作，不暴露底层组合件。
- 对外 API 优先简单，复杂度封装在拥有行为的模块内部；派生配置、内部安全边界和底层组合件不应暴露成业务必填项。
- 生命周期、协议细节、await/inbox、队列、重试、清理、默认超时、序列化、header、adapter/store/codec 组合等机制性复杂度应下沉到框架层。
- 业务层只接触必要业务参数，例如 proto、key、field、callback、handler 或领域对象；不要让业务直接拼底层 header、inbox、store、codec 或 transport。
- 确实需要底层 escape hatch 时，只能作为调试、测试或适配层能力，不能成为默认业务入口；必须在 README 或注释中说明边界。
- 新框架设计阶段默认破坏式收敛到唯一业务门面；不为旧实验 API 保留 wrapper、deprecated 入口或重复命名层，除非用户明确要求迁移窗口。

## 开发期 AI 工具边界

- MCP、CLI、LLMs.txt、skills、checklist 和模板属于开发期 AI 辅助体系；不要因为它们能辅助生成代码，就把对应协议或工具链做成业务运行时主路径。
- LLM provider 调用门面属于 `aifd-llm`；业务 prompt、provider key、额度策略、审计落库和 prompt 管理平台留在业务项目或业务服务。
- `aifd-llm` 的 provider 扩展参数在适配器拥有的 HTTP 边界透传并测试；只检查中立 `Request.ProviderOptions` 不算验证生效。扩展不得覆盖通用字段（即使当前未输出该字段），不可序列化或冲突参数在发请求前失败；业务项目不得复制 HTTP 参数补丁来绕过框架适配器。
- 普通桌面或 Web 前端默认使用固定 React/Vue/原生组件和普通业务数据；A2UI、XCard、运行时 Agent 动态 UI 只能在用户明确要求该产品形态时单独设计。
- Ant Design 组件知识查询归 [ant-design-ui-development](../ant-design-ui-development/SKILL.md)，Wails 桌面壳归 [desktop-app-development](../desktop-app-development/SKILL.md)；不要在业务项目中新增无边界的 UI command runtime。
- Wails CLI 是 `aifd-tools` 的按需工具，使用 `aifd tools install --tool wails` 管理；不要把 Wails CLI 路径硬编码进示例，也不要把它加入默认服务端工具安装集合。
- NSIS、dmg/pkg、Sparkle、AppImage 等安装包生态属于桌面发布环境；首版只做显式检查和文档说明，不在无授权情况下自动安装系统级工具。
- 桌面端运行时通信、PB/WS、缓存、数据分析、本地文件访问和整包更新流程应放 Go 后端；JS/React 保持 UI 和轻量交互态。

## 命名与 API

- 公开框架命名必须像业务可理解的后端能力，而不是实现细节或部署形态。runtime 公共语言使用 `service/user`；业务服务按领域命名，例如游戏示例的 `game/world` 或工具项目的 `workspace/index/jobs`。
- 不把游戏词汇提升为框架概念。`Player/Game/World` 只属于游戏示例或业务 proto；非游戏项目使用自己的领域名，框架使用 `User` 和 `UID` 表达用户业务文档；其他非用户状态按真实领域归属 service module。
- `Get*` 表示读取且不触发创建、加载或 hook；`Calc*` 表示纯计算；`Validate*` 只做校验；`Is/Can/Has/Should*` 返回布尔；`Build/Make/New*` 构建对象；`Clone*` 必须表达真实拷贝边界。
- 公开 API 重命名时同步更新 contracts、示例、文档、rules、测试和静态旧名检查，避免框架语言出现两套入口。
- 新增 helper 前先搜索现有工具、模块内惯例和框架公共抽象。

## 组织与状态

- 同类行为函数放在一起；一个文件内选择唯一顺序，不混用调用顺序、字母顺序和临时方便顺序。
- 推荐函数顺序：入口注册、纯计算、读取、判断、校验、构建、持久化、私有 helper。
- 状态类对象要说明 key、value、真相源、生命周期、恢复方式和同步关系。
- 只读少量字段时优先窄快照，不为方便读取整对象深拷贝；slice、map、repeated 等内部引用返回前必须明确是否复制。
- 不把锁持到 RPC、存储、网络、复杂排序或日志大对象拼接之后；已持锁 helper 使用 `Unsafe` 后缀，并在注释写清锁要求。

## 持久化

- 先明确存储模型是同步强一致、异步 final-intent、命令历史还是普通缓存；不同模型不能混用同一套安全承诺。
- 账号映射、唯一 ID 分配、绑定查重这类即时一致性能力可以直接使用 Redis Store；不要强行接入异步 rdb 队列。
- 异步持久化队列必须说明合并 entity、Save/Delete 合并规则、flush/close 语义和失败处理；`Save -> Delete`、新绑定对象 `Delete -> Save`、重复 Save 合并和删除后同一 saver Save no-op 都是必测边界。
- final-intent journal 只保证单个持久化 entity 的最终保存/删除意图，不保证中间命令历史、跨 entity 事务顺序或中间 TTL 副作用。
- Redis 连接类失败可以 retry；marshal、权限、类型错误等不可恢复错误不能进入 retry 或 replay journal。

## 注释、日志和错误

- 导出函数、模块入口、异步回调、持久化变更、配置解释 helper 都应有简洁注释，说明行为和边界。
- 启动、恢复、热更、跨期、发奖、RPC 入口、持久化发布、异步回调等关键流程要有可观测锚点。
- logging 是允许全局门面的横切例外；使用 `aifd-observe/logging` 的 `log.Info`、`log.Warn`、`log.Error`、`log.Stat` 和 `log.Statf`，不要新增 `share.Log` 或 `share.Stat`。
- 运行日志本地优先写 stdout/file；observe 查询端通过 RPC 拉各进程 observe-agent，agent 只按逻辑 file 名 tail/filter 预注册的本地日志或 stat 文件。高频日志不要通过 RPC 主动推送。
- 嵌入式 observe-agent 属于 control plane，不参与业务热路径；tail/filter 查询必须有 timeout、max bytes、max lines 和 max concurrency，并发满时直接拒绝，不排队反压业务进程。
- 业务打点使用 `log.Stat(name, fields...)` 或 `log.Statf(format, args...)` 生成 `KindStat`，输出目标由入口层 sink/router 决定，业务模块不要判断 observe 还是外部打点服务器。
- 日志字段要能定位主体、周期、动作、来源、结果和失败原因；不要打印完整大对象或隐私数据。
- 稳定错误语义集中管理；发生点用错误包装补充现场上下文。
- 对外错误码、内部哨兵错误和日志原因分层表达，不用泛泛的 `failed` 替代业务可解释失败。

## 验证与沉淀

- 代码改动后优先跑受影响模块测试；命名收敛要补静态旧名检查。
- 文档或 skill 改动至少检查 YAML frontmatter、触发描述和索引是否仍准确。
- 每次分析问题、review 或完成改动后，判断是否产生可复用规则。
- 可复用规则先归并到已有 skill、checklist 或模板；只有没有合适承载点时才新增 skill。
- 最终交付必须说明“项目级经验”：新增、变更、合并建议，或明确没有发现。

## 相关文档

- [skills 索引](../README.md)
- [模块边界与职责依据](../../../docs/module-boundary.md)
- [项目与 Feature 代码评审](../runtime-feature-review/SKILL.md)
