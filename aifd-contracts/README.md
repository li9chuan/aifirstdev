# aifd-contracts

## 定位

`aifd-contracts` 保存 aiFirstDev 各模块描述服务、模块、能力和框架级日志时需要的
最小公共契约。

## 负责

- 稳定的模块描述和能力元数据。
- 基础设施模块之间共享的服务类型和模块类型枚举。
- 需要长期保持安全兼容的最小接口。

## 不负责

- 业务 `.proto` 文件、生成的 pb 代码或消息 schema。
- 配置表、Luban 项目、生成的 cfgtable 代码或 feature 常量。
- Player、World、DBPlayer、lifecycle、RPC 路由、持久化或玩法类型。

## 业务项目如何使用

业务项目需要用框架中立的方式描述 feature、service 或 adapter 时可以 import
此模块。不要把项目专属枚举或 feature 状态放到这里。

## 更新边界

这是 workspace 中的最小共享契约，由框架根版本统一发布，不单独升模块版本。
破坏性变更需同步消费方并记录迁移；当前 0.x 阶段的兼容策略以[更新策略](../docs/update-policy.md)为准。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [模块边界与职责依据](../docs/module-boundary.md)
- [架构与修改面路由](../aifd-rules/skills/architecture-and-surface-routing/SKILL.md)
