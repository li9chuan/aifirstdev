# aifd-runtime

Runtime 提供 User 的驻留与加载、用户模块接线，以及 Service 的恢复、启动和关闭顺序。业务 PB、Redis key、跨用户关系和任务真相源由业务项目或显式组合插件拥有。

## User

`user.Runtime[T]` 管理一个 UID 到一个 `User[T]` 的缓存。同一用户的设备共享这个对象；读取离线用户不会建立连接。`T` 是业务拥有的强类型 protobuf，主数据使用 `rdb.SetSaver[T]`。

```go
store, err := user.NewStore(user.StoreOptions[*pb.DBUser]{
    DB: db, Key: userKey, New: newDBUser,
})
users := user.NewRuntime(reg, store, user.NewManager[*pb.DBUser](module), user.WithBus(bus))
err = users.RegisterRPC()
go func() { _ = users.Run(serviceContext) }()
```

入口代码处理构造、注册及 Run 的错误，并在关闭时先停止业务任务与 RPC，再关闭 UserRuntime，最后排空 RDB。构造不隐式启动租约循环。晚于模块构造才打开的 Bus 可在启动阶段用 `BindBus` 绑定一次。

| API | 契约 |
|---|---|
| `LoadUser(ctx, uid)` | 加载已有用户，同 UID 并发加载合并，完成 OnLoad 后发布缓存；不存在返回 ErrNotFound |
| `GetUser(uid)` | 纯缓存查询，不加载、不创建、不运行 hook |
| `CreateUser(ctx, uid)` | 注册或初始化流程显式创建；已存在返回 ErrAlreadyExists |
| `NewSession(ctx, uid)` | 构造同一 rpc.Session 的内部调用上下文，绑定 Request/Push；没有客户端 ref、inbox 或响应通道 |
| `RenewConnection(s)` | 只续期 Proxy 注入的已绑定有效连接；不会根据 UID 建立连接 |
| `OnlineConnections(uid)` | 当前有效连接数量，不加载用户 |
| `Run/Close` | 显式租约循环和关闭；Close 不关闭共享 DB |

主 PB 的存在性默认定义用户存在性。如果业务身份源独立、主 PB 可以缺省，Store 的可选 `Exists(ctx, uid)` 只在主 PB 不存在时验证业务用户；加载保留默认 PB 在内存，不保存空文档。这个回调不用于创建用户或恢复在线状态。

User module 提供 Descriptor、OnLoad 和 RegisterRPC。OnLoad 在一次成功驻留中执行一次；登录与断线按连接租约处理。模块通过 ModuleContext.Runtime 取得同一门面，不建立第二份主 User 或客户端管理器。

## 用户路由与数据块

Go 1.27 的具体类型泛型方法提供用户路由：

```go
err := users.Client("MceRead", func(s *rpc.Session, u *user.User[*pb.DBUser], req *pb.MceRead) {
    // Client 已验证有效连接引用，并加载可信 s.UID() 的同一 User。
    feature.Read(s, u, req)
})
err = users.Server("ReadUser", func(s *rpc.Session, u *user.User[*pb.DBUser], req *pb.ReadUser) {
    // Server 可以处理离线用户，不建立在线状态。
    feature.ReadOffline(s, u, req)
})
```

普通后台任务直接 `LoadUser(ctx, uid)`。管理员先校验权限，再加载目标 UID，保留原 Session 的操作者与响应通道。全局/非用户路由仍使用 `rpc.Client/Server/Admin/Stream`。

`Read/Snapshot` 读取主 PB；`Mutate(ctx, fn)` 修改私有候选并保存入队，回调错误、panic、取消或队列拒绝保留已发布值。回调短暂执行，不保留 PB 引用。`Save(ctx)` 只负责保存主块。

`RLock/RUnlock/Lock/Unlock` 协调用户拥有的多个业务块；各块仍有自己的强类型 Saver。锁顺序为 User → Saver，锁内不做 RPC、AI、行情等外部工作。已持 User 锁时不能调用自动加锁的方法。`SaverUnsafe` 仅供存储适配层在正确锁范围内使用，不是普通 handler 入口。多块保存不提供数据库事务。

## 连接与通信

UserRuntime 组合 Proxy 的 ClientSessions。WS socket、握手/登录包认证、可信 metadata、原连接回包及 UID PubSub 由 Proxy 拥有；RPC 业务只接收 rpc.Session。Open/Close 生命周期路由不加载主 User。

本次成功或错误使用 `s.Response/Error`，回到原连接和原 inbox。独立用户事件使用 `s.Push/PushTo`，广播到目标 UID 的全部在线设备，inbox 为 0。业务不手工操作 inbox；重连读取最新事实。详情与必验场景见 [业务调用规则](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md)。

## Service

`service.Service` 管理 Module 的 Recover、RegisterRPC、Start、Close 顺序，恢复完成前不接受状态推进。关系、索引、投递、聚合和后台任务由业务或插件自己的 service module 拥有。同进程与拆进程部署使用相同状态归属。

可选插件由应用入口显式组合，import 无副作用。框架不拥有具体业务字段、玩法、配置表或持久化 key。更新边界仍是整体根模块发布，不保留历史实验 API 包装层。

## 相关文档

- [Runtime 模块开发](../aifd-rules/skills/runtime-module-development/SKILL.md)
- [业务 Feature 开发](../aifd-rules/skills/feature-development/SKILL.md)
- [组织原则](../docs/scope-runtime.md)
