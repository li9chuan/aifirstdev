---
name: runtime-module-development
description: 开发 aifd-runtime、业务 runtime feature 模块或可选功能插件时使用。适用于 user/service runtime、User 加载与锁、module lifecycle、恢复、tick、storage、用户路由和连接租约接线，以及显式组合插件。
---

# Runtime 模块开发

这个 skill 负责 runtime/module 接入规则，不负责具体业务玩法规则。遇到“runtime 是否鸡肋”或
“这段逻辑应不应该进入 runtime”这类边界争议时，先用本 skill 判断 runtime 是否真正拥有该
生命周期或模块编排职责。

## 模块形态

- User module：当前用户的业务文档及数据块，实现 `runtime/user.Module[T]`。
- Service module：服务级恢复、RPC 注册、启动、关闭，以及跨用户的索引、聚合、投递、关系、广播或异步任务，实现 `runtime/service.Module`。
- Optional feature module/plugin：官方或业务可选能力包，例如 mail、friend、guild、ranking；通过 runtime module 接口显式组合，不进入 runtime 核心。
- Lifecycle runner：恢复完成后推进状态机或定时任务。
- `service/user` 是 runtime 公开命名；`game/world`、`workspace/index/jobs` 等由业务应用按领域定义。`local` 只用于 local bus、local transport、memory/local adapter 等本地实现语义。

## 落地顺序

1. 先确定状态归属：user runtime、service lifecycle、service module 或业务 feature；业务应用自己命名承载服务。
2. 定义最小接口和数据模型，不提前实现完整业务闭环。
3. 接入 config hook、schema/rpc route、rdb saver 和 observe sink。
4. 实现启动恢复，再允许 tick、RPC 写入或 lifecycle 推进。
5. 补最小测试：模块注册、恢复、写入拒绝、tick 条件、memory adapter。

## 边界

- `aifd-runtime` 拥有通用生命周期：service/user runtime 服务启动、恢复顺序、tick gate、
  module 顺序、驻留 User 管理、user/service module hook，以及业务 proto 与 `aifd-rdb`
  的通用接线。
- `aifd-runtime` 不拥有业务 proto 字段、具体玩法规则、配置表语义、Redis key 命名、socket、robot 场景或 observe 聚合口径。
- UserRuntime 组合 `aifd-proxy/runtime.ClientSessions` 的租约能力；实际 WS、身份注入、Open/Close 桥及下行投递仍归 Proxy。业务使用 Runtime 的续期入口，不建立平行 User/session manager。
- 业务 feature 放在业务项目 `features/`，通过 runtime 公开接口接入；feature 自己定义状态归属、
  配置解释、协议名、持久化 key 和指标名。
- RPC handler 保持薄，调用拥有状态的 module；如果 handler 开始持有业务状态、读配置并改库，
  应收回到对应 user/service module。
- 启动恢复、热更和状态推进要有明确顺序和日志锚点；恢复完成前不要开放 tick、RPC 写入或外部推送。
- `service` 是逻辑生命周期边界，不等于必须拆成物理微服务；all-in-one、拆分进程和 hybrid 部署
  都不能改变状态真相源归属。
- 邮件、好友、公会、排行榜这类可选能力的代码归属是插件包或业务项目，不是 `aifd-runtime`；
  需要跨 root 真相源时，在插件自己的 service module 中实现。

## 可选功能插件

- 可选功能插件实现 `runtime/service.Module` 或 `runtime/user.Module[T]`，由应用入口显式 `NewModule`
  后传入 `service.NewService` 或 `user.NewManager`。
- 插件包 import 必须无副作用；禁止在 `init()` 中自动注册 route、启动 worker、创建 Redis/NATS/client
  或修改全局 registry。
- `NewModule(opts)` 只构造对象并保存依赖；状态恢复、RPC 注册、worker/tick 启动只能发生在
  `Recover`、`RegisterRPC`、`Start` 等 runtime 生命周期内。
- 插件依赖只能通过 `Options` 注入，例如 `DB`、`Observe`、`Config`、`Clock`、RPC client 或 bus；
  不在插件内部偷读全局配置或自行创建运行期连接。
- 插件自己的 proto/generated、Redis/RDB key、RPC topic、指标名由插件包或业务项目拥有，不进入
  `aifd-runtime` 或 `aifd-schema` 核心。
- 推荐代码结构是 `aifd-plugins/<name>/service`、`aifd-plugins/<name>/user`、`aifd-plugins/<name>/proto`
  和 `aifd-plugins/<name>/generated`；默认属于框架根 Go module，只有实际独立消费或发布需求才评估拆分。
- mail、friend、guild、ranking 的跨 root 真相源优先放在插件自己的 service module，例如
  `aifd-plugins/mail/service`；root 侧只保留视图、缓存、红点或在线推送。

## User 生命周期

涉及邮件插件的 owner、Game/user 接入、收件资格、领取 CD、恢复或缓存淘汰时，读取 [Mail owner 接入规则](references/mail-owner.md)。

- 框架提供 `user.Runtime[T]`、`User[T]` 和 `UID()`；业务服务仍按真实领域命名。
- 唯一加载入口为 `LoadUser(ctx, uid)`，同 UID 首次加载合并，完成 `OnLoad` 后才发布缓存；失败可以重试。`GetUser(uid)` 只查驻留缓存，不触发 I/O 或 hook。
- 用户创建使用 `CreateUser(ctx, uid)`，读取不能隐式创建。若主 PB 可缺省、用户身份存在于其他业务源，使用 Store 的 `Exists` 判定；它只能验证存在性，不能借机保存空 PB。
- 用户路由使用 Runtime 的 `Client/Server` 自动注入；普通服务或全局路由仍用 RPC 原有入口。管理员目标 UID 显式校验并加载。
- `OnLoad` 只在成功驻留时执行一次，没有反复登录的 `OnActivate`。在线状态按各连接租约维护，不能根据驻留 User 推断。
- `Run(ctx)` 显式接入服务生命周期；关闭 Runtime 会拒绝新加载并等待已接纳的初始化，不关闭共享 DB。保存队列由 RDB 生命周期排空。
- User 协调锁先于 Saver 锁；已持协调锁时不调用自动加锁方法。`Mutate` 回调错误、panic、Context 取消或保存拒绝不发布候选值；保存入队不等于数据库事务。
- 涉及 RPC 回包、Push、内部 Session 或心跳时读取 [用户调用与多设备投递](../feature-development/references/rpc-user-and-multidevice.md)。

## 何时使用 runtime

- 多个 feature 需要共享一致的激活、恢复、active root、module hook 或停服关闭顺序。
- root 根文档由业务 proto 承载，但需要框架统一提供锁、快照和 rdb 保存能力。
- service module 状态需要先恢复再注册 RPC、启动 tick 或接受事件。
- 服务入口希望只组合 module，而不是手写一串无顺序约束的 Init/Register/Start。
- 非游戏工具也需要当前用户/工作区数据域和共享索引/任务域的生命周期边界。

## 何时不要放进 runtime

- 只是一个稳定的 Redis saver、RPC adapter、proxy codec 或 robot client 行为。
- 逻辑依赖具体业务协议、具体配置表、玩法字段或运营规则。
- 只在某个业务项目中出现，且不能跨 feature 复用生命周期约束。
- 为了示例方便而把 `MceAuth`、背包、竞技场等业务名提升为框架概念。
- 为了通用化而把 Player 改成 Role，或把 Player/World 当成框架默认命名。
- 为了避免部署复杂度而把所有跨主体关系塞进 root 根文档；轻量部署应通过 all-in-one wiring
  解决，不通过混淆状态归属解决。

## 骨架验收

- 可编译。
- 可注册。
- 可用 memory/local adapter 测试。
- 恢复完成前拒绝状态推进。
- 不要求首批实现完整业务闭环。

## 相关文档

- [skills 索引](../README.md)
- [runtime 组织原则](../../../docs/scope-runtime.md)
- [Runtime 模块用法](../../../aifd-runtime/README.md)
- [Feature 目录模板](../../templates/user-service-feature.md)
