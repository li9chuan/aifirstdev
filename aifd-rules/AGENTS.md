# aiFirstDev Agent 规则

AI 辅助框架开发的高频规则：

- 不确定用哪个 skill 时，先读取 `skills/README.md`。
- 进入代码编辑、重排、问题分析或 review 后修代码时，先使用 `project-coding-constraints`。
- 涉及 `aifd-account`、登录身份、账号 Redis 模型、token payload 或业务项目接入账号中心时，读取 `account-identity-store-workflow`。
- 编辑前先选择最小职责归属模块。
- 对外 API 优先简单，复杂度封装在拥有行为的模块内部；不要把派生配置、内部安全边界或底层组合件暴露成业务必填项。
- 依赖方向保持单向：contracts/llm -> config/observe/proxy/robot/rpc/rdb/schema -> runtime -> project。
- 模块分类和 `aifd-core` 准入以工作区 `docs/module-boundary.md` 为准；当前不创建 `aifd-core`，不新增 `share`、`common`、`utils` 或泛化工具包。
- 不要把 game、player、world、DBPlayer、具体 feature 概念、业务 `.proto`、生成的 pb 代码、真实 Luban 表、生成的 cfgtable 代码、业务 prompt 或 provider key 加入 `aifd-config`、`aifd-llm`、`aifd-observe`、`aifd-proxy`、`aifd-robot`、`aifd-rpc`、`aifd-rdb` 或 `aifd-schema`。
- proxy-broker 是 `aifd-proxy/adapters/nats/broker` 的显式入口能力；game/world/其他业务服只作为 NATS client 接入，不隐式启动 broker。
- logging 全局门面只归 `aifd-observe/logging`；不要新增 `share.Log`/`share.Stat`，高频运行日志本地优先，不通过业务 RPC 主动推送；嵌入式 observe-agent 属于 control plane，必须有 timeout、max bytes、max lines 和 max concurrency，不参与业务热路径。
- 业务项目编写 `features/`；生成的 registry 必须可重建。
- runtime 变更必须在拥有行为的最低层包补测试。
- 配置 reload 使用 prepare-then-commit。
- Proto 和 config 工作流模块只提供规则、检查和 adapter；业务项目拥有具体 schema 和配置表真相。
- Lifecycle runner 只有在恢复完成后才能推进全局状态。
- Wails / React / Ant Design 桌面应用先用 `desktop-app-development`，组件实现再叠加 `ant-design-ui-development`。
- Ant Design MCP、CLI、LLMs.txt、X Skills 是开发期 AI 辅助体系；普通桌面示例运行时不默认接 A2UI、XCard 或 Agent。
- Wails CLI 由 `aifd-tools` 按需安装和诊断；NSIS 属于 Windows 安装器环境，不作为默认工具自动安装。
- 桌面运行时通信、缓存、数据分析和整包更新流程默认放 Go binding 后端，React 只负责 UI 展示。
- 产生文件修改后，最终交付需要给提交描述时给出 `提交描述`；提交标题第一个方括号写主模块标签，例如 `[aifd-rules]`。需要详细正文格式时读取 `svn-commit-description`。
- 最终交付中的提交描述必须提供独立的 `text` 代码块；代码块内只放可直接复制的提交标题/正文，不包含“提交描述：”标签、解释文字或 Markdown 项目符号。
