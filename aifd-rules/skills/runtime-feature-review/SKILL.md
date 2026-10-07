---
name: runtime-feature-review
description: 审查 runtime 或业务 feature 代码时使用。适用于检查恢复顺序、RPC 接入、tick/goroutine、配置、持久化幂等、日志、测试覆盖和模块职责边界，并要求 findings-first 输出。
---

# Runtime Feature 评审

这个 skill 只做通用 runtime/feature 代码评审，不内置具体业务语义。

## 输出格式

- 先列 findings，按严重程度排序。
- 每条 finding 指向文件和行号，说明风险、触发条件和修复方向。
- 没有问题时明确说明，并列出未覆盖验证项。
- 摘要和改动说明放在 findings 之后。

## 检查点

- 模块是否放在拥有职责的 runtime 或业务 feature 中。
- RPC handler、proxy route、CLI command 是否保持薄。
- 启动恢复是否在配置和依赖准备后执行。
- 恢复完成前是否拒绝写入、tick 或 lifecycle 推进。
- 持久化 save/delete/load 是否幂等，key 是否包含必要生命周期维度。
- 配置读取是否走 owning module 的解释 API。
- tick/goroutine 是否可停止，是否避免泄漏和重复启动。
- 日志是否覆盖关键状态迁移、失败原因和幂等 skip。
- 测试是否覆盖 memory/local adapter、恢复、重复请求和错误路径。

## 边界

- 不用具体业务规则替代通用工程审查。
- 如果审查需要业务语义，要求额外读取业务项目自己的规则或需求文档。
- 不因为测试依赖外部服务失败就直接判定逻辑失败；要区分环境问题和编译/行为问题。
