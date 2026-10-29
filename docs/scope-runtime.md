# AIFD User / Service Runtime

`runtime/user` 按 UID 管理用户业务文档、并发加载、模块 OnLoad、锁、快照和持久化接线，组合 Proxy 的连接租约。实际 socket 与传输仍由 Proxy 管理。`runtime/service` 管理服务模块的恢复、注册、启动和关闭。

一个业务 owner 内同 UID 对应一个驻留 User，多设备共享它。主 PB 与持久化 key 由业务定义。`LoadUser(ctx, uid)` 仅加载已有用户；`CreateUser` 显式创建；`GetUser` 只查缓存。加载不产生在线副作用，关闭一条连接不销毁业务 User。详见 [Runtime 用法](../aifd-runtime/README.md)。

User module 只拥有当前用户的数据块；跨用户的关系、索引、投递、聚合、排行和任务由业务或插件的 service module 拥有。工作区、分析会话等非用户状态按实际领域归属 service module，不借用户连接寿命管理。

可选能力采用 `aifd-plugins/<name>/user`、`service`、`proto`、`generated`，由入口显式组合。import 不注册路由、启动 worker 或创建连接；Recover、RegisterRPC、Start、Close 承担其生命周期。

Service 是逻辑边界。all-in-one、split 和 hybrid 只改变接线，不改变状态真相源归属；框架不要求复杂服务编排。Wails 前端调用 Go binding 的业务门面，不接触 Runtime 或传输对象。

恢复顺序由宿主实现：`service.Service` 按传入顺序执行 hook，不自动阻止跳过 Recover 调用 Start，
也不自动按 Depends 排序。带明确恢复门禁的定时推进使用 `lifecycle.Runner`；具体约束见 [Runtime 用法](../aifd-runtime/README.md#service)。

## 相关文档

- [Runtime 模块开发](../aifd-rules/skills/runtime-module-development/SKILL.md)
- [User / Service Feature 模板](../aifd-rules/templates/user-service-feature.md)
- [用户调用与多设备投递](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md)
