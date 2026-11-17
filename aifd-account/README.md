# aifd-account

**v0.8.0 提供账号与身份升级**。它包含独立凭据验证包和可选账号中心；
外部账号系统可以直接接入 Proxy/Runtime，无需创建 AIFD 账号或更换 token 格式。

## 职责与接入

| 层 | 入口 | 拥有的事实 |
|---|---|---|
| 凭据验证 | [credential](credential/README.md) | 渠道 proof 验证为 Principal，不解释 account/session |
| 可选账号中心 | [ServiceAPI](service.go)、[proto](proto/account_api.proto) | 账号、身份集合、登录会话、撤销与票据 |
| 连接认证 | [Proxy](../aifd-proxy/README.md#认证有效期与后续重检) | 受信身份、到期和重检；业务准入由宿主负责 |

账号中心不管理业务 UID、owner、realm、角色、服务器或租户权限。宿主维护账号到业务用户的映射、
授权 HTTP/RPC 路由，并提供 TokenIssuer。API proto 是消息契约，不自动注册端点。

用 NewService(store, verifier, issuer, Options{...}) 组合服务。必填 BackendID、Audience
及至少 32 字节的 IndexKey；后者用于 HMAC 身份索引，直接轮换需要迁移索引。
默认 SessionTTL 为 24 小时，TicketTTL 为 30 分钟，票据期限不超过会话剩余期限。
自带模式的连接 ValidUntil 取已验证会话期限；ticket 到期约束新建连接和直接凭 ticket 续票，
不会把已建立连接的期限改为 ticket 期限。外部无 session 模式的连接期限不晚于原 token 的 exp。
MemoryStore 用于本地示例；服务不启动网络、定时任务或隐藏全局注册。

## 账号动作

- Register 验证首个身份及 RegistrationPolicy，只创建账号，不登录；未提供策略默认拒绝。
- Login 只登录已存在账号，创建独立 session 并签发票据；账号不存在直接失败。
- BindIdentity 验证候选身份并重新证明当前账号；UnbindIdentity 也要求重新证明当前账号。
  两者比较 ExpectedRevision，拒绝身份抢占、最后一个身份解绑及过期修改。
- UnbindIdentity 只改变身份集合，不撤销会话。RevokeSession 撤销一个 session；
  RevokeAllSessions 按预期 epoch 推进账号会话版本；首版没有按绑定撤销。
- ChangeAccountStatus 是受信管理操作，比较 epoch；禁用及恢复不复活旧会话。
- IssueLoginTicket 为已认证会话换票，不延长会话绝对期限；知道 SessionRef 不代表已认证。

敏感操作的 actor 来自宿主受信上下文，不能接受客户端任意填写的 account/session。
验证码和网页授权用 BuildVerificationBinding 绑定用途、当前会话、身份版本及解绑目标。
小程序 code 直接向微信交换，不声称普通 code 具备挑战关联；敏感动作仍需重新证明当前账号。

## 只读身份查询与离线导入

`Service.ResolveAccount(ctx, credential.Principal{AuthorityID: authority, Subject: subject})`
只查询已登记身份，核对索引与主档成员关系，不建号、不开会话、不调用渠道验证器。禁用账号仍返回其状态。
它是受信管理/恢复入口；宿主必须授权查询，传入 Principal 不代表调用者已经证明身份，不能直接映射成公开登录接口。

离线导入可使用 `LoginRefFromPrincipal(indexKey, principal)` 构造与 Service 相同的索引；密钥至少 32 字节，
必须与目标 Service 配置一致。继续调用 Store.ResolveAccount/CreateAccount，不能由宿主复制索引算法。

MemoryStore/RedisStore 的可选 `CreatedAccountCleaner.DeleteCreatedAccount(ctx, ref, accountID)`
只用于离线导入回滚。宿主先隔离全部账号写入者，并用批次记录证明该账号由本批新建。只允许 ACTIVE、
单一匹配身份、revision=1、epoch=1 的主档；改变过身份、状态或撤销版本即拒绝。它不检查“从未登录”，
不能作为在线删除或判断账号未被使用的接口。删除后残留 session 因主档不存在而不可用，ID 不复用。

Redis 先条件删除主档，再条件删除索引，跨分片不原子；删除结果不明返回 ErrResultUnknown，不隐式重放。
需要恢复时保留原批次，重新确认停写与实际状态；主档已删、原索引仍匹配时可继续清理，二者均不存在时返回 false。
已有的 Store/ServiceAPI 接口不增加必需方法，既有实现无需为这项可选能力修改接口。

## Redis 模型与故障语义

NewRedisStore(redis.UniversalClient, RedisStoreOptions{Space: "example"}) 使用独立小 key：

| Key | Value / 生命周期 |
|---|---|
| `account:<space>:login:<authority_id>:<subject_digest>` | 十进制 account_id，唯一索引，无 TTL |
| `account:<space>:act:<account_id>` | DBAccount protobuf，身份集合、revision、epoch，无 TTL |
| `account:<space>:sess:<session_id>` | DBLoginSession protobuf，按绝对期限设置 TTL |
| `account:<space>:seq:act` | INCR 序列，允许空洞 |

身份摘要是服务端索引密钥下、绑定 authority 与 subject 的 HMAC-SHA256；主档、索引和 session
不保存手机号/OpenID 明文。connector_id 选择验证器，authority_id 标识可信身份空间，二者不能混用。

注册先写候选主档，再用 SET NX 争夺唯一索引。绑定先占索引，再比较更新主档；解绑先更新主档，
再删索引。读取同时检查索引及主档成员关系；中间状态拒绝该身份进入，不影响其他有效身份和会话。
只对单个主档做 WATCH/CAS，不把跨分片多 key 写入描述为事务。

写命令禁用不确定结果自动重放，只有确定的 WATCH 冲突可以重新比较。ErrResultUnknown 表示可能已提交，
不得当作确定失败或盲目执行反向补偿。残留索引恢复须暂停相关写入、隔离旧执行者，再核对索引和主档；
框架不提供在线“见到不一致就删除”的修复循环。序列空洞和孤儿候选允许留待受控清理。

宿主拥有 Redis client 生命周期，保证主节点无缓存读取、账号/索引不被驱逐及所需持久化与复制配置。
包装 client 必须保留 go-redis 的 NoRetry 语义；不承诺异步复制在故障切换时零丢失。

## 示例与验证

- [Minigame](../aifd-examples/minigame/README.md)：签发与查询同一份会话事实，开发客户端向服务端取票。
- [外部账号](../aifd-examples/external-auth/README.md)：原 token、字符串账号及可选 session 直接进入 UserRuntime。
- 运行 go test -race ./aifd-account/... ./aifd-proxy/... ./aifd-examples/external-auth。
- 设置 AIFD_ACCOUNT_REDIS_ADDR 和逗号分隔的 AIFD_ACCOUNT_REDIS_CLUSTER_ADDRS 才覆盖真实
  standalone/Cluster、跨主节点及响应丢失；未设置会明确跳过。

旧 API、存盘模型和示例协议不兼容本次升级，不提供旧数据迁移。见
[v0.8.0 迁移与验收说明](../docs/releases/v0.8.0.md)及
[开发约束](../aifd-rules/skills/account-identity-store-workflow/SKILL.md)。
