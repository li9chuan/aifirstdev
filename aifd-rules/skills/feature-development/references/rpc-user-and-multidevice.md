# 用户调用与多设备投递

涉及用户路由、消息 PB、客户端调用或用户事件时读取。业务协议属于业务项目；框架只定义通用调用与连接能力。

## 用户入口

- `user.Runtime[T]` 是 UID 的业务门面。`LoadUser(ctx, uid)` 仅加载已有用户，并合并同 UID 的并发加载；不存在返回 `ErrNotFound`。主 PB 可缺省的项目配置 `StoreOptions.Exists` 验证业务身份，读取不写空文档。注册或初始化流程显式使用 `CreateUser`。
- `LoadedFromStore` 只是初次读取主 PB 的结果，存储适配层用它初始化缺省块；不代表当前落库状态、在线状态或身份有效期。
- `GetUser(uid)` 只查驻留缓存。`OnLoad` 在一次成功驻留中执行一次；读取用户不建立在线状态。
- 普通用户路由使用 `users.Client(topic, func(s, user, req))` 或 `users.Server(...)`；框架注入同一 User。Client 要求 Proxy 注入的有效连接引用，Server 可以处理离线用户。
- 后台直接用 `LoadUser(ctx, uid)`。只有需要 Request、Push 等能力时才使用 `users.NewSession(ctx, uid)`；内部 Session 没有原请求回包通道，不代表客户端在线。
- 管理员先校验权限，再显式加载目标 UID；原 Session 保留操作者和回包通道，通知目标使用 `PushTo`。
- User 协调锁先于各块 Saver 锁。已持 User 锁时不能再调用自行加锁的 `Read/Mutate/Save/Snapshot`；多块操作由业务模块封装，不承诺数据库事务。

## 请求结果和用户事件

| 意图 | API | 投递与消费 |
|---|---|---|
| 本次请求成功或失败 | `s.Response(pb)` / `s.Error(code, message)` | 原请求连接、原 inbox；完成一次 Call/Request |
| 当前用户的共享事实变化 | `s.Push(eventPB)` | 当前 UID 全部在线连接，包括发起端；inbox 为 0，独立事件处理 |
| 指定用户的事件 | `s.PushTo(uid, eventPB)` | 指定 UID 全部在线连接；业务负责目标权限 |

inbox 只标识一条连接上的一次请求，不是 UID 消息通道、业务版本或幂等键。业务不能拼接 inbox、回包 header 或路由，也不能用 Push 广播请求回包。不同设备可以同时使用相同 inbox。

新增 PB 必须在注释中说明请求、响应或事件，以及接收范围；不能只凭 `Mse` 前缀判断。需要跨设备同步时使用独立事件 PB。例如 A 修改数据后：操作结果 `Response` 给 A；`Push(DataChanged)` 给 A/B/C。两者可能交错到达，客户端事件处理器不能结束等待中的 Call。

共享事实事件由业务维护版本和刷新规则；重复或旧事件不能回退已展示事实。重连后重新读取最新事实。在线 Push 不提供可靠离线消息；需要可靠消息时由业务维护持久记录、游标和确认。

## 连接与心跳

每条连接以 Proxy 启动实例 ID 和连接 ID 绑定租约。关闭或到期的旧引用不能被迟到心跳复活；加载用户不会续期。业务心跳先校验业务条件，再 `users.RenewConnection(s)`，最后返回自己的强类型 PB。没有业务心跳时可以启用 `WithDefaultHeartbeat()`。

认证成功后启动 SDK 心跳，Probe 使用有期限的强类型 Request；关闭客户端时停止。Go Probe 必须遵守 Context。失联通知只触发一次；显式停止或关闭不报失联。Proxy 本地 Pong 不能证明业务服存活。业务服重启后客户端必须重新鉴权连接，不能只按 UID 恢复在线状态。

## 验证

通信改动覆盖两设备相同 inbox 的成功和错误回包隔离、UID 事件到达全部设备及不触发 Call、跨 Proxy 普通 PubSub、单连接关闭、旧引用续期拒绝、Proxy 强杀到期和业务服重启。跳过真实 NATS 或进程检查须明确报告。协议生成物从源 `.proto` 重建。
