# aifd-schema

## 定位

`aifd-schema` 是 aiFirstDev 的 proto/message 工作流层，保存框架中立的消息描述，
检查 schema 兼容性，并把入站消息绑定到 `aifd-rpc`。

## 负责

- Message、field、registry 和 codec 契约。
- 内存 schema registry、兼容性 validator 和 RPC binder。
- JSON 测试 codec 和 protoc 工作流 adapter 包边界。
- 框架自带的通用 proto 源文件，例如 `proto/aifd/proxy/proxy.proto`。

## 不负责

- 业务 `.proto` 文件、生成的 pb 包或真实 message id 目录。
- feature 专属请求、响应、存储或玩法语义。
- RPC transport、runtime module 归属或 proxy delivery 行为。

## 业务项目如何使用

业务项目把具体 schema 和生成代码留在自己的源码树中，再把 descriptor 注册到
`aifd-schema`，用于检查、生成 route 绑定和 AI 可读的兼容性审查。

客户端与 proxy 的通用 PB 帧头使用 `aifd.proxy.MsgHead`。业务项目可以基于该
proto 生成自己的前后端代码，但生成物仍应留在业务项目或对应 generated 目录中。

## 更新边界

Schema 升级应保持 descriptor 和 registry 契约。兼容性检查只有在新规则可执行且已
文档化时，才应变得更严格。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Schema 与 Proto 工作流](../aifd-rules/skills/schema-proto-workflow/SKILL.md)
- [生成工具与命令状态](../aifd-tools/README.md)
