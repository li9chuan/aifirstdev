---
name: schema-proto-workflow
description: 处理 proto/schema 工作流时使用。适用于 aifd-schema、框架默认 PB、业务项目 proto、generated pb 边界、消息兼容性、字段真相、生成联动和 RPC 绑定检查。
---

# Schema 与 Proto 工作流

这个 skill 处理 proto/schema 本身和联动分析，不替代具体 feature 实现。

## 文件归属

- 框架默认 PB 放在 `aifd-schema/proto/aifd/<domain>/vN/<domain>.proto`。
- 示例 proto 放在 `aifd-examples/<example>/proto/`。
- 业务 proto 放在业务项目自己的 `proto/`。
- 生成的 pb 代码放在业务项目或明确的 generated 目录，不放进 `aifd-contracts`、`aifd-runtime`、`aifd-rpc`。
- 业务 route 常量和 generated route registry 放在业务项目或业务 generated 目录；框架只消费 descriptor 或窄元数据做检查。
- `aifd-schema` 和 `aifd-rpc` 不 import 业务 generated pb，也不拥有业务 route registry 的运行时真相。

## 字段真相约束

- 协议只放跨进程通信必需的事实，不重复配置表可直接读取的展示值。
- 存盘 proto 只保留恢复所需的最小状态；没有审计、回放、历史展示需求时不加流水结构。
- 同一业务含义只允许一个真相源；不要同时保存原始流水和可稳定推导的累计值。
- WS 的 `aifd.proxy.RPCError` body 只是 `Status/errorMessage` Header 的线上编码，由 proxy codec 转换；业务使用 `Session.Error`。客户端在业务 PB 解码前识别错误 topic，不能以空 body 判断成败。
- 客户端需要的本次操作结果可以下发；长期状态优先由服务端真相和配置推导。

## 兼容性与破坏式修改

- 已发布或明确要求兼容的协议：不删除已发布字段号，不复用已删除字段号，不改变已有字段的 wire shape、repeated/optional 语义或消息方向。
- 未发布、示例或当前明确允许破坏式演进的协议：可以删除、改名或重排字段语义，但必须在同一改动内同步 proto、generated pb、RPC/proxy/robot/示例调用方和测试。
- 破坏式改动不强制保留 deprecated 字段或 reserved 字段号；只有需要防止未来误复用、保留迁移窗口或对外说明时才加 reserved/deprecated。
- 审查时先判断协议稳定级别；不能默认把示例 proto 当作已发布兼容面。
- 改消息名、路由名或生成包路径时，要检查 RPC 绑定、proxy 转发、robot 回调和示例。
- 框架默认 PB 文件按协议域命名，不按单个 message 命名。
- session-first typed binding 的公共入口应使用 session/caller context；不要把 `uid`、role、player 或 account 这类业务主体做成框架 handler 签名概念。

## 工作流

1. 先判断是框架默认 PB、示例 proto 还是业务 proto。
2. 明确消息方向、拥有服务、路由名、route 常量归属和生成物位置。
3. 对照字段真相约束检查每个字段是否必要。
4. 画出联动链：schema descriptor、业务 route registry、RPC route、proxy、robot、示例和业务项目。
5. 若改动破坏兼容，先确认是否属于允许 break 的未发布/示例协议；若是，检查联动和测试是否完整，若不是，要求版本升级或 migration 说明。

## Route 绑定边界

- AIFD 可以提供 route/schema 绑定检查、descriptor 校验和 typed dispatcher helper。
- 业务项目拥有具体 route 常量、route -> message 绑定和 generated registry。
- 不要新增框架级通用业务 route manifest。需要审计时，由业务项目生成可重建 artifact，再交给 AIFD 工具检查。
- typed dispatcher helper 只处理 session/caller context、route、request/response、error 和 codec 这类基础设施问题；业务身份从 caller context 或业务请求中解释。

## 相关文档

- [skills 索引](../README.md)
- [Schema 模块用法](../../../aifd-schema/README.md)
- [生成工具与命令状态](../../../aifd-tools/README.md)
