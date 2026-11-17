---
name: account-identity-store-workflow
description: "规划、实现或审查凭据验证、可选账号中心、身份绑定、账号 Redis、登录会话撤销，以及外部账号接入 Proxy/Runtime 时使用。"
---

# Account 身份与存储工作流

用法与 API 以 [Account](../../../aifd-account/README.md)、[凭据验证](../../../aifd-account/credential/README.md)
及 [Proxy](../../../aifd-proxy/README.md#认证有效期与后续重检) 为准。本规则对应 v0.8.1，不据此推断旧版能力。

## 分层与事实源

- credential 只验证 proof，返回 authority/subject，不依赖 Account 根包、accountpb、aifd-auth 或 Runtime。
- Account 可选，拥有账号、身份集合与登录会话；外部系统可保留原 token、字符串账号及可选 session。
- Proxy 接收中立 Identity/AuthContext；业务 UID、租户、权限、角色、服务器与准入由宿主适配。
- connector_id 是验证器配置入口，authority_id 是可信身份空间；跨渠道复用主体要有权威映射依据。
  微信 UnionID 缺失不能静默降级或自动合并账号。
- 账号索引/主档使用服务端 HMAC 摘要，不存原始手机号/OpenID；短期挑战敏感数据由渠道 Store 管理。

## 只读查询与离线维护

- Service.ResolveAccount 接收受信 Principal，只读索引与主档；不认证、不建号、不开会话，禁用状态仍可查询。宿主先授权，不能把返回值直接当登录成功。
- 离线索引复用 LoginRefFromPrincipal，与目标 Service 共用 IndexKey；不复制 HMAC 算法，不把旧无密钥摘要当新索引。
- DeleteCreatedAccount 仅供已隔离全部账号写入者的离线批次清理，校验本批创建证据、原身份/账号以及初始 revision/epoch/status。初始状态不证明从未登录；不得用于在线删除。
- 清理跨 key 分步完成；结果不明先核实，再在同一维护隔离条件下恢复。保留批次，不自动反向补偿，不重用已删除账号 ID。

## 账号动作

- 注册与登录分开；默认拒绝注册，必须注入政策。注册不登录，登录不创建账号。
- 身份变更核对当前会话、重新认证及 identity revision；解绑不能移除最后一个身份。
- 解绑不撤销会话。首版支持单 session 删除、账号 epoch 推进，不增加 binding ID 或按绑定撤销。
- 全账号撤销和状态变更比较预期 epoch；旧请求不能重新撤销随后创建的会话，恢复账号不复活旧会话。
- actor 来自受信上下文。Service 方法、SessionRef 和 ChallengeBinding 自身不是路由授权或身份凭证。
- 票据签名格式由宿主拥有；验签、backend/audience/time 校验、权威会话检查与业务准入要完整组合。

## 存储与恢复

- 复用小 key：login 直接指向 account_id，主档含身份集合/revision/epoch，session 独立带 TTL。
- 跨分片分步写入不是多 key 事务；读取核对索引与主档成员关系，部分写入拒绝该身份继续进入。
- 写入结果不明保留事实，不盲目重放或反向补偿；只对明确 CAS 冲突重试比较。
- 不用全局 hash tag 把系统压在一个 Cluster 分片，不引入全局大 HSET。
- 残留索引恢复须隔离旧执行者、暂停相关写入再核对事实；不提供在线猜测式删除。
- 宿主负责主节点无缓存读取、不驱逐账号/索引及部署持久化。包装器保留 NoRetry 语义。

## 渠道与连接

- 验证码先占冷却窗口后发送，明确成功后激活；校验次数与消费原子化。
- 网页 OAuth 一次性消费 state、用途和 scope；回调地址来自配置白名单。
- 小程序 code 直接交换，不能伪称有服务端挑战关联；敏感变更仍重新证明 actor。
- 初次 Authenticate 完成整套检查，不立即重复 Revalidate。后续显式选择 ExpiryOnly、Periodic、PerRequest，
  与 session 有无正交；缺失策略拒绝启动。
- 所有连接有绝对截止时间；空闲、纯推送也受检查。重检失败/超时先本地失效，晚返回不能恢复。
- AuthContext 由 Gateway 从已认证连接生成；header 本身不是来源证明，业务仍依赖可信来源与连接租约门禁。

## 验收与联动

- 公开 API、proto 或存储模型变化，同步 README、示例、客户端、生成物与迁移说明。
- 真实 Cluster 覆盖跨主节点、并发、部分写入、响应丢失与受控恢复，不以 miniredis 替代。
- 连接覆盖握手/packet、local/NATS、到期/撤销、超时晚返回、最终发送门禁和空闲连接。
- 外部接入验证三种策略乘有/无 session，保留原身份、权限和准入边界，并扫描独立包依赖。
- 本地渠道协议测试与真实微信/短信联调分开；发布和业务接入分别验收。
