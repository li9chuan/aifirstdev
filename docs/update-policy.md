# 更新策略

框架作为一套产品交付，业务项目通过一个 Go module 版本锁定整套框架。
当前仍处于早期开发阶段，实验 API 默认破坏式收敛，不承诺稳定 API。

## 统一发布与后续迭代

- 框架根 module 为 `github.com/li9chuan/aifirstdev`。基础设施、LLM、账号中心、CLI 和官方插件共享 `go.mod/go.sum`，一次发布只打一个根标签。
- 职责目录、包名、现有 import 路径、单向依赖和显式组合边界不变；统一 module 不会自动启用可选能力。
- `aifd-examples/minigame` 和 `aifd-examples/desktool` 保留独立 `go.mod`，通过 `go.work` 参与本地开发，用于消费验证和构建环境隔离，不单独发布模块标签。
- 从 `v0.2.0` 起统一发布根 module。已经发布的版本标签不移动或覆盖。
- 业务项目统一升级根 module，不能分别锁定或升级 rpc、rdb 等框架包。只有出现真实的独立消费、发布周期或依赖隔离需求，才重新评估拆分。
- Go 依赖版本的真相源是业务项目的 `go.mod/go.sum`。`aifd.lock` 只维护 `tools.*` 开发工具版本。
- Go、Wails、前端和工具链继续使用各自明确版本。框架统一发版不要求第三方版本号一致。

发布前验证根 module、两个示例、生成物和独立消费项目。独立消费检查必须关闭 workspace，且不能携带指向工作区的 `replace`。
可在临时本地 Go proxy 中验证候选根版本的获取及 `go install ...@版本`；这只能证明候选包可消费。
源码和根标签发布后，仍需从真实远端复验获取与 CLI 安装。桌面安装器和原生更新另行验收。

仓库内 minigame 示例只依赖根 module，保留 `replace github.com/li9chuan/aifirstdev => ../..`，用于未发布阶段和关闭 workspace 时测试当前源码。
业务项目消费已发布版本时不能复制这个本地替换。

## 模块分类

- `aifd-contracts` 只承载跨模块最小契约。
- `aifd-*` 基础设施根模块在统一版本下按职责维护，例如 config、llm、observe、proxy、robot、rpc、rdb、runtime 和 schema。
- `aifd-plugins/*` 只承载 mail、friend、guild、ranking 这类可选业务能力插件，由应用入口显式组合。
- `aifd-tools`、`aifd-rules` 和 `aifd-examples` 分别承载工具、规则和示例，不作为业务运行时基础依赖。

当前不创建 `aifd-core`。只有一个小能力满足无领域语义、无运行时依赖、至少两个基础设施根模块生产代码稳定复用，并且不能合理归属现有模块时，才考虑未来的 `aifd-core/<pkg>`。完整规则见 [module-boundary.md](module-boundary.md)。

## 项目规则

- 业务代码应放在 `features/`。
- 生成的注册表应放在 `generated/`，并且可以安全重建。
- 业务代码不应 import `rpc/internal`、`rdb/internal` 或 runtime 内部包。
- feature 测试默认使用本地 RPC 和 memory RDB adapter。
- 业务 `.proto`、生成的 pb 代码、Luban 表和生成的 cfgtable 代码留在业务项目中。
- 业务 prompt、provider key、额度策略、审计落库和 prompt 管理平台留在业务项目中。

## 保留的占位入口

`aifd update` 和 `aifd update --check` 当前只说明整套升级方式，不检查远端版本、不下载、不修改依赖。
命令输出会明确没有执行检查或升级；不支持按单个职责模块更新。
实际升级通过 Go 命令完成，不在本次 module 合并中实现新的升级器。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [工具能力与命令状态](../aifd-tools/README.md)
- [项目与 Feature 代码评审](../aifd-rules/skills/runtime-feature-review/SKILL.md)
