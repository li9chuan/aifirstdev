# aiFirstDev Skills Map

这份索引用于选择 aiFirstDev 的项目级和框架级 skill。具体业务项目可以在此基础上追加自己的业务 skill。

## 默认选择顺序

审查项目或模块时，直接使用[项目与 Feature 代码评审](runtime-feature-review/SKILL.md)，由它按范围选择规则和检查项。
开发与修改任务按下列顺序选择：

1. 不确定修改面：先用 [architecture-and-surface-routing](architecture-and-surface-routing/SKILL.md)。
2. 进入代码编辑、重排、问题分析或 review 后修代码：叠加 [project-coding-constraints](project-coding-constraints/SKILL.md)。
3. 复杂框架能力或复杂业务 feature：使用 [complex-backend-workflow](complex-backend-workflow/SKILL.md) 冻结 L0-L5。
4. 明确主修改面后，切到对应模块 skill。

## 通用项目级 skill

- [runtime-feature-review](runtime-feature-review/SKILL.md)
  - 项目级代码审查入口；按范围引用已有规则与清单，检查模块边界、运行行为、测试和文档一致性，并以 findings-first 输出。
- [project-coding-constraints](project-coding-constraints/SKILL.md)
  - 用于职责边界、最小业务门面、框架复杂度下沉、service/user 命名、文件组织、锁与拷贝、持久化、日志、错误和项目经验沉淀；遇到 `share` / `common` / `core` / `kit` 类小工具归属争议时必须读取，并参考工作区 [模块边界](../../docs/module-boundary.md)。
- [architecture-and-surface-routing](architecture-and-surface-routing/SKILL.md)
  - 用于判断需求应落在 `contracts/llm/config/schema/rpc/rdb/runtime/proxy/observe/robot/tools/rules/examples` 还是业务项目；runtime 边界、基础工具集、`aifd-core` 准入、observe-agent 和 `aifd-auth` / `aifd-account` / `aifd-proxy` / `aifd-llm` 归属不清时先用它定主修改面。
- [complex-backend-workflow](complex-backend-workflow/SKILL.md)
  - 用于复杂能力设计、术语冻结、真相源、恢复模型、分批计划和验收门禁。
- [svn-commit-description](svn-commit-description/SKILL.md)
  - 用于任务产生文件修改后的最终交付，生成“提交描述”；提交标题第一个方括号必须写主模块标签，例如 `[aifd-tools]`、`[aifd-rules]`、`[desktool]`。

## 框架模块 skill

- [schema-proto-workflow](schema-proto-workflow/SKILL.md)
  - 用于框架默认 PB、业务 proto、generated pb 边界、消息兼容性和 RPC 绑定联动。
- [account-identity-store-workflow](account-identity-store-workflow/SKILL.md)
  - 用于 `aifd-account`、登录身份、`account_id`、provider/subject_hash、账号 Redis key、token payload、账号创建/禁用/并发创建和业务项目接入账号中心；确认 account 只管理登录账号，不承载业务 uid、owner、realm、角色或服务器归属。
- [config-table-workflow](config-table-workflow/SKILL.md)
  - 用于 `aifd-config`、Luban/cfgtable 适配、typed bundle、validator、manifest diff 和热更边界。
- [service-entrypoint-development](service-entrypoint-development/SKILL.md)
  - 用于 CLI、service、gateway、worker、example 的启动链和进程级 wiring；proxy/game/world
    split-mode 入口和 config/observe 注入模板优先读取它。
- [runtime-module-development](runtime-module-development/SKILL.md)
  - 用于 `aifd-runtime` 的 user/service/User/module/lifecycle 和业务 runtime feature 接入；重点判断 runtime 只拥有生命周期和模块编排，不拥有具体业务状态与协议。
- [robot-module-development](robot-module-development/SKILL.md)
  - 用于 `aifd-robot` 的 AgentModule、行为树、消息路由、callback、scenario 和 report。
- [desktop-app-development](desktop-app-development/SKILL.md)
  - 用于 Wails + React + Ant Design 桌面应用、`desktool` 示例、Go binding、登录流程、公开主界面、登录后工作台、Go/JS 通信边界、整包更新和 account/proxy/business 服务边界。
- [ant-design-ui-development](ant-design-ui-development/SKILL.md)
  - 用于 Ant Design React UI 开发；需要查询组件 API、demo、token、semantic DOM、doctor/lint/migrate、MCP、CLI、LLMs.txt 或 X Skills 边界时读取。
- [feature-development](feature-development/SKILL.md)
  - 用于业务项目 feature 的 user/service 归属、目录、协议配置和测试接入。

## 前端和桌面开发

- 桌面应用先读取 [desktop-app-development](desktop-app-development/SKILL.md)，再按 UI 组件需求叠加 [ant-design-ui-development](ant-design-ui-development/SKILL.md)。
- Ant Design MCP、CLI、LLMs.txt 和 X Skills 都是开发期辅助；普通 desktool 运行时不默认接 A2UI、XCard 或在线 Agent。
- Wails CLI 由 `aifd-tools` 按需管理，使用 `aifd tools install --tool wails`；NSIS 是 Windows 安装器环境，首版不自动安装。
- 桌面应用中的 WS/PB、缓存、数据分析和更新下载/安装启动默认放 Go binding 后端，React 只负责 UI 展示和轻量交互态。

## 交付格式

- 只要本轮产生文件修改，并且需要给提交描述，最终交付中给出 `提交描述`。需要详细正文格式时读取 [svn-commit-description](svn-commit-description/SKILL.md)。
- 提交标题第一个方括号写主模块标签。

## 不迁移内容

- 具体玩法、活动、排行榜业务规则和压测脚本。
- C++ 专项实现规则。
- 旧项目专属路径、服务名、配置表名和协议名。
- 没有 aiFirstDev 框架模块承载的业务公共系统。

## 相关文档

- [规则与资料入口](../README.md)
- [评审检查清单](../checklists/review.md)
- [User / Service Feature 模板](../templates/user-service-feature.md)
