# 更新策略

框架拆分的目标是让业务项目可以独立更新基础设施，而不需要重写 feature 代码。
当前仍处于早期开发阶段，实验 API 默认破坏式收敛，不承诺稳定 API。

## 首版发布与后续迭代

- 最终模块前缀为 `github.com/li9chuan/aifirstdev/`，首版使用 `v0.1.0`，表示可获取的发布快照，不表示接口稳定。
- 保留单仓库、多 Go module 结构。每个 module 使用目录前缀标签，例如 `aifd-runtime/v0.1.0`、`aifd-plugins/mail/v0.1.0`；首版标签指向同一提交。根目录 `v0.1.0` 只表示整体快照，不能替代子模块标签。
- 本地协作使用 `go.work`；外部消费者通过 `go get <module>@v0.1.0` 引入模块，不能依赖本仓库的 workspace 或本地 `replace`。模块依赖必须能从发布标签解析。
- 发布后按主分支持续迭代，可通过 commit 固定后续快照。是否增发标签或维护多个分支按实际需求决定，不预设稳定版时间表或多版本兼容承诺。
- 标签发布后不移动或覆盖；修复已发布快照时创建新提交，并另行决定新的发布版本。
- 框架自身的发布安排不影响第三方依赖锁定。`tools.*`、Go 和前端依赖仍使用各自明确版本。

发布前先验证完整 workspace、生成物及独立消费项目的依赖解析；推送源码和模块标签后，再从真实远端验证 `go get` 与 CLI 安装。发布源码不等于桌面安装器已经完成安装或更新验收。

## 模块分类

- `aifd-contracts` 只承载跨模块最小契约。
- `aifd-*` 基础设施根模块按职责独立演进，例如 config、llm、observe、proxy、robot、rpc、rdb、runtime 和 schema。
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

`aifd update` 当前只输出计划信息，不检查远端版本，不下载或更新框架，也不运行 migration。
它返回成功不代表依赖已经检查或更新，不应作为自动升级或发布门禁使用。例如：

```text
aifd update --check
aifd update rpc
aifd update rdb
aifd update schema
aifd update config
aifd update observe
aifd update proxy
aifd update robot
aifd update runtime
aifd update rules
aifd proto check
aifd config check
```

此入口暂时保留，实际自动更新能力在后续发布前另行评估，不在本次地址统一和首版发布中实现。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [工具能力与命令状态](../aifd-tools/README.md)
- [项目与 Feature 代码评审](../aifd-rules/skills/runtime-feature-review/SKILL.md)
