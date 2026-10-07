---
name: config-table-workflow
description: 处理配置表、Luban、cfgtable 适配和 aifd-config 工作流时使用。适用于配置真相源、typed bundle、loader、validator、manifest diff、热更、业务生成配置边界和配置读取规则。
---

# 配置表与 Luban 工作流

这个 skill 处理配置工作流和配置真相，不替代具体 feature 规则。

## 文件归属

- 框架配置抽象放在 `aifd-config`。
- 框架示例配置可以放在 `aifd-examples/<example>`。
- 真实 Luban 工程、Excel、生成 cfgtable 和业务配置校验留在业务项目。
- `aifd-contracts`、`aifd-runtime`、`aifd-rpc` 不直接沉淀具体业务表结构。

## 读取与适配

- 已知表名时，先确认表入口、row type、key 类型和读取 API。
- 不知道表名时，先从业务概念定位 domain 关键词，再找入口和生成类型。
- 业务项目构建自己的 typed config bundle，例如 `*cfg.Tables` 或业务 `ConfigBundle`，再交给 `aifd-config` 管理生命周期。
- `aifd-config` 不要求也不接收 cfgtable 到通用 row/table 中间结构的全量映射。
- 运行期读取 API 应放在拥有 feature 的模块中，不散落直接读生成表。

## 校验与热更

- 热更使用 prepare-then-commit：校验和派生索引/视图全部成功后再发布。
- cfgtable 与派生索引需要强一致时，应放入同一个业务 typed bundle，在发布前一起构建完成。
- normalize、remap、补默认值和派生索引构建属于 loader/builder；validator 和 prepare hook 只读检查 bundle。
- 失败时保留旧 bundle，不发布半热更状态。
- 必填字段、唯一键、引用完整性和派生索引在 validator 中集中表达。
- 配置值不应重复写入协议或存盘，除非存在明确冻结、隐藏或性能原因。

## 工作流

1. 判定配置源属于框架、示例还是业务项目。
2. 找到表入口、key、row type、索引和使用方。
3. 明确哪些值是配置真相，哪些是运行期状态。
4. 补充 bundle builder、loader、validator、manifest 或热更联动。
5. 用 memory/json adapter 覆盖配置测试；真实 Luban 适配在业务项目验证。
