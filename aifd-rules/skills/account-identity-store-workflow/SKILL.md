---
name: account-identity-store-workflow
description: "当规划、实现或审查 aifd-account、登录身份、account_id、provider/subject_hash、账号 Redis key、token payload、账号创建/禁用/并发创建、迁移 provisioning 或业务项目接入账号中心时使用。用于保持账号中心只管理登录账号，不承载业务 uid、owner、realm、角色或服务器归属。"
---

# Account 身份与存储工作流

`aifd-account` 是纯账号中心：只负责把已验证的登录身份解析为全局 `account_id`，并生成与 provider 无关的 auth payload/token。业务项目自己决定 `account_id` 如何映射业务用户、角色、服务器或业务域。

## 边界规则

- account 层只拥有登录身份、账号主档、账号状态和 token payload。
- 不在 account 层引入 `owner_id`、`realm_id`、业务 `uid`、角色列表、服务器归属、last owner 或业务用户映射。
- 业务项目可以选择 `业务 uid = account_id`，也可以维护自己的角色/服务器/业务用户映射；该映射不回写到 account 中心。
- account 核心不 import 业务 proto，不保存业务事实，不感知业务 Redis key。
- public API 表达登录、解析、禁用状态和 token 语义；一次性迁移清理、数据修复和业务 provisioning 补偿不要沉淀成 account 公共接口。

## 登录身份

- `LoginRef{Provider, SubjectHash}` 表示已规范化且已验证的登录身份引用。
- `subject_hash` 使用 `sha256("<provider>:<normalized_subject>")` 这类 provider 绑定算法生成。
- Redis key、protobuf 存盘值、日志和报告不保存手机号、openid、邮箱等明文 subject。
- 用户密码、验证码、OAuth code、旧系统凭据校验属于 credential adapter；account store 只接收已验证结果。
- `autoCreate=false` 必须返回账号缺失错误，不得隐式创建空账号。

## Redis 存储模型

固定使用 Redis Cluster 友好的小 key：

- `account:login:<provider>:<subject_hash>` -> decimal `account_id`
- `account:act:<account_id>` -> `DBAccount` protobuf bytes
- `account:seq:act` -> `INCR`

不要使用全局大 HSET 保存 login index 或账号主档。不要恢复 `account:identity`、`account:accounts`、`account:owner*`、`seq:owner` 这类 owner/realm 模型 key。

## 创建与并发

- 解析流程先 `GET login key`，命中后加载 `account:act:<id>` 并校验账号状态。
- 未命中且允许创建时，先 `INCR account:seq:act`，写入 `account:act:<new_id>`，再 `SETNX login key <new_id>`。
- `SETNX` 明确返回未写入表示并发已创建；删除本次孤儿 `act` 后返回已存在账号。`SETNX` 返回错误属于结果不明，必须保留 `act`，避免 login key 已写入但账号主档被删除。`seq` 允许空洞。
- 禁用账号必须在加载 account record 后拒绝登录。
- 损坏或缺失的 `act` 不能被静默当作新账号覆盖，应返回可诊断错误。

## 迁移和业务接入

- 迁移工具可以在受控流程里调用低层 store 的 resolve/create 能力，但补偿清理应留在迁移工具 adapter，不进入 account 公共 store 接口。
- 业务项目接入时，只从登录结果读取 `account_id`、provider、`subject_hash` 和 token 时间；业务 uid、权限、角色、数据分区由业务服务自己管理。
- 示例项目如果需要 uid，可临时令 demo uid 等于 `account_id`；不要把示例里的 game/player/world 概念提升到 account 层。

## 验证门禁

- 测试覆盖重复登录、并发创建、`autoCreate=false`、禁用账号、损坏 `act` 和 login key 不含明文 subject。
- 改动公开类型、proto 或 Redis key 模型时，同步检查 README、示例、测试和生成物。
- 静态扫描确认没有重新引入 `OwnerID`、`OwnerRef`、`RealmID`、`owner_id`、`realm_id`、`account:owner` 或 `seq:owner`。

## 相关文档

- [skills 索引](../README.md)
- [Account 模块用法](../../../aifd-account/README.md)
- [Auth Ticket 用法](../../../aifd-auth/README.md)
