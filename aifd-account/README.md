# aifd-account

`aifd-account` 是纯账号中心，负责把已验证的 provider credential 解析为全局
`account_id`，并签发 provider-neutral login payload token。

账号中心不管理业务 `uid`、owner、realm、角色列表、last owner 或具体业务归属。
业务项目如果需要一个账号对应多个角色、服务器或业务用户，应在自己的服务中维护映射。

账号中心核心不 import 业务 proto；业务项目通过 `TokenIssuer` 把 `AuthPayload`
转换成自己的登录 payload protobuf 后签名。

## 接入顺序

1. 提供 `Verifier` 校验并规范化 credential；框架自带 `DemoVerifier` 仅供示例和测试，不是生产密码/OAuth/短信验证器。
2. 用 `NewService(store, verifier, issuer, Options{})` 构造服务，由 `TokenIssuer` 调用业务的 payload 序列化与签名。
3. 调用 `Service.Login` 取得账号与 token；`AutoCreate=false` 时不存在即失败。禁用账号、损坏主档和存储错误不会当作新账号重建。

服务自身不注册 HTTP 或 RPC 入口。最小接线见 [minigame account](../aifd-examples/minigame/internal/app/account.go)，
完整类型与登录流程见 [types.go](types.go) 和 [service.go](service.go)。

## Redis 存储

生产入口使用 `NewRedisStore(redis.UniversalClient, RedisStoreOptions{})` 直接落 Redis。
Redis key 模型固定为：

- `account:login:<provider>:<subject_hash>` -> decimal `account_id`
- `account:act:<account_id>` -> `DBAccount` protobuf bytes
- `account:seq:act` -> `INCR`

`subject_hash` 由 `sha256("<provider>:<normalized_subject>")` 生成，key 和持久值不保存手机号、
openid 等明文 subject。Redis client 的创建、关闭和重连策略由应用入口负责，
`aifd-account` 只使用传入 client。`MemoryStore` 只用于单测和本地 demo。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [账号身份与存储工作流](../aifd-rules/skills/account-identity-store-workflow/SKILL.md)
- [签名 Ticket](../aifd-auth/README.md)
