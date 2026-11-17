# 外部账号接入

本示例把原签发方 token 接入 Proxy 和 UserRuntime，不依赖 Account 根包、accountpb 或 aifd-auth。
Adapter 接受原 TokenVerifier，验证租户、权限和有效期，再由业务 Resolve 映射到 UID。
它不替换 token 格式、不把 UUID 账号改成 AIFD account_id、不强制创建登录 session。

宿主将 Handshake 接入 HandshakeHandler，或者在自己的 packet 登录函数中调用 Authenticate。
ExpiryOnly 不查询原系统撤销状态；Periodic、PerRequest 注入 Check，在初次认证及后续重检时查询
原系统事实。Adapter.Mode 和 WebSocket.Config.AuthCheckMode 应使用同一配置值。

go test -race ./aifd-examples/external-auth 通过真实 WebSocket 进入 UserRuntime，覆盖三种模式乘
有/无 session 的六种组合、原 token 验签、租户/权限/业务准入、到期与撤销后的连接及租约清理。
测试签发器只是本地 fixture；生产接入原系统验证与状态查询组件。
