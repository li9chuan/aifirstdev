# aifd-auth

`aifd-auth` 保存账号中心和 proxy 共享的认证基础件。

当前 `token` 包提供 HMAC payload token：账号中心对业务自己的登录 payload
做确定性序列化后签名，proxy 使用同一份 payload bytes 和 token 做本地验签，
不在登录热路径依赖账号中心。

`aifd-auth` 不 import 业务 proto，也不保存 `accountID`、`ownerID` 这类业务字段；
这些基础数据由业务登录 payload 承载，例如 minigame 的 `MAuthPayload`。

## 接入与 key 更换

账号入口用 `NewSigner(Key{ID, Secret}, Options{})` 构造签名器，对业务 payload bytes 调用
`Sign(payload, SignOptions{IssuedAt, ExpiresAt})`。Proxy 用 `NewVerifier([]Key{...}, Options{})`
构造验签器，对相同 bytes 调用 `Verify(payload, token)`；业务层仍需解释身份和权限。

当前已支持 key ID 和多 key 验签，可由宿主在过渡期保留旧 key、用新 key 签发；构造时会拷贝 key，
配置变更需要显式重建并替换实例。尚无自动轮换、JWKS、非对称签名或撤销查询。
Token 不嵌入也不加密业务 payload，传输保护使用 HTTPS/WSS。精确 API 见 [token.go](token/token.go)。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [账号身份与存储工作流](../aifd-rules/skills/account-identity-store-workflow/SKILL.md)
- [账号中心](../aifd-account/README.md)
