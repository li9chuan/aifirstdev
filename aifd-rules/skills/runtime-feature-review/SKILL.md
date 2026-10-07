---
name: runtime-feature-review
description: 审查 aiFirstDev 项目、框架模块或业务 feature 时使用。按审查范围选择已有规则和清单，检查职责边界、运行行为、测试及文档一致性，并以 findings-first 输出。
---

# 项目与 Feature 代码评审

这是 aiFirstDev 的项目级代码审查入口，也适用于单个 runtime 或业务 feature；具体业务语义以业务项目需求为准。

## 阅读与审查路径

1. 确认用户目标、审查范围和主修改模块；从该模块 README 了解能力与限制。职责不清时查阅[模块边界](../../../docs/module-boundary.md)和[修改面路由](../architecture-and-surface-routing/SKILL.md)。
2. 使用[项目级编码约束](../project-coding-constraints/SKILL.md)和[评审检查清单](../../checklists/review.md)，只检查与本次范围相关的项。协议、配置、账号等专项规则从[skills 索引](../README.md)按需选择，不默认加载全部 skill。
3. 沿实际调用和状态流核对实现、测试与文档；特别检查业务变更是否无必要地修改框架，是否重复已有能力，以及规则是否有明确归属和可达入口。文档可发现性问题与实际运行缺陷分别说明。
4. 根据风险运行相关验证并记录证据；全仓构建入口见[项目首页](../../../README.zh-CN.md#构建与验证)。局部审查不因清单包含发布或桌面项而自动扩大为全套验收。

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

## 相关文档

- [skills 索引](../README.md)
- [评审检查清单](../../checklists/review.md)
