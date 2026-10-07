# aifd-auth

`aifd-auth` 保存账号中心和 proxy 共享的认证基础件。

当前 `token` 包提供 HMAC payload token：账号中心对业务自己的登录 payload
做确定性序列化后签名，proxy 使用同一份 payload bytes 和 token 做本地验签，
不在登录热路径依赖账号中心。

`aifd-auth` 不 import 业务 proto，也不保存 `accountID`、`ownerID` 这类业务字段；
这些基础数据由业务登录 payload 承载，例如 minigame 的 `MAuthPayload`。

后续如果需要非对称签名、JWKS 或 key rotation，仍应保持业务 feature 只接触登录结果，
不直接处理签名细节。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [账号身份与存储工作流](../aifd-rules/skills/account-identity-store-workflow/SKILL.md)
- [账号中心](../aifd-account/README.md)
