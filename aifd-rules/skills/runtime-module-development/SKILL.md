---
name: runtime-module-development
description: 开发 aifd-runtime、业务 runtime feature 模块或可选功能插件时使用。适用于 service/root runtime、Root、module lifecycle、Init、Register、恢复、tick、storage、RPC skeleton、配置 hook、feature 接入顺序，以及 mail/friend/guild/ranking 等显式组合插件。
---

# Runtime 模块开发

这个 skill 负责 runtime/module 接入规则，不负责具体业务玩法规则。遇到“runtime 是否鸡肋”或
“这段逻辑应不应该进入 runtime”这类边界争议时，先用本 skill 判断 runtime 是否真正拥有该
生命周期或模块编排职责。

## 模块形态

- Root module：当前玩家、用户、工作区或分析会话等业务根文档内聚状态，实现 `runtime/root.Module[T]`。
- Service module：服务级恢复、RPC 注册、启动、关闭，以及跨 root 的索引、聚合、投递、关系、广播或异步任务，实现 `runtime/service.Module`。
- Optional feature module/plugin：官方或业务可选能力包，例如 mail、friend、guild、ranking；通过 runtime module 接口显式组合，不进入 runtime 核心。
- Lifecycle runner：恢复完成后推进状态机或定时任务。
- `service/root` 是 runtime 公开命名；`game/world`、`workspace/index/jobs` 等由业务应用按领域定义。`local` 只用于 local bus、local transport、memory/local adapter 等本地实现语义。

## 落地顺序

1. 先确定状态归属：root runtime、service lifecycle、service module 或业务 feature；业务应用自己命名承载服务。
2. 定义最小接口和数据模型，不提前实现完整业务闭环。
3. 接入 config hook、schema/rpc route、rdb saver 和 observe sink。
4. 实现启动恢复，再允许 tick、RPC 写入或 lifecycle 推进。
5. 补最小测试：模块注册、恢复、写入拒绝、tick 条件、memory adapter。

## 边界

- `aifd-runtime` 拥有通用生命周期：service/root runtime 服务启动、恢复顺序、tick gate、
  module 顺序、active root 管理、root/service module hook，以及业务根 proto 与 `aifd-rdb`
  的通用接线。
- `aifd-runtime` 不拥有业务 proto 字段、具体玩法规则、配置表语义、Redis key 命名、proxy
  客户端 session、robot 场景或 observe 聚合口径。
- 业务 feature 放在业务项目 `features/`，通过 runtime 公开接口接入；feature 自己定义状态归属、
  配置解释、协议名、持久化 key 和指标名。
- RPC handler 保持薄，调用拥有状态的 module；如果 handler 开始持有业务状态、读配置并改库，
  应收回到对应 root/service module。
- 启动恢复、热更和状态推进要有明确顺序和日志锚点；恢复完成前不要开放 tick、RPC 写入或外部推送。
- `service` 是逻辑生命周期边界，不等于必须拆成物理微服务；all-in-one、拆分进程和 hybrid 部署
  都不能改变状态真相源归属。
- 邮件、好友、公会、排行榜这类可选能力的代码归属是插件包或业务项目，不是 `aifd-runtime`；
  需要跨 root 真相源时，在插件自己的 service module 中实现。

## 可选功能插件

- 可选功能插件实现 `runtime/service.Module` 或 `runtime/root.Module[T]`，由应用入口显式 `NewModule`
  后传入 `service.NewService` 或 `root.NewManager`。
- 插件包 import 必须无副作用；禁止在 `init()` 中自动注册 route、启动 worker、创建 Redis/NATS/client
  或修改全局 registry。
- `NewModule(opts)` 只构造对象并保存依赖；状态恢复、RPC 注册、worker/tick 启动只能发生在
  `Recover`、`RegisterRPC`、`Start` 等 runtime 生命周期内。
- 插件依赖只能通过 `Options` 注入，例如 `DB`、`Observe`、`Config`、`Clock`、RPC client 或 bus；
  不在插件内部偷读全局配置或自行创建运行期连接。
- 插件自己的 proto/generated、Redis/RDB key、RPC topic、指标名由插件包或业务项目拥有，不进入
  `aifd-runtime` 或 `aifd-schema` 核心。
- 推荐代码结构是 `aifd-plugins/<name>/service`、`aifd-plugins/<name>/root`、`aifd-plugins/<name>/proto`
  和 `aifd-plugins/<name>/generated`；是否拆独立 Go module 按实现阶段决定。
- mail、friend、guild、ranking 的跨 root 真相源优先放在插件自己的 service module，例如
  `aifd-plugins/mail/service`；root 侧只保留视图、缓存、红点或在线推送。

## Root 生命周期

- `Root` 是 root runtime 中被 runtime 管理的业务根文档对象；不要再用 `Subject` 这种过宽泛命名，
  也不要把 `Player/Game/World` 这类业务词汇提升为框架公共概念。
- 业务服务由应用按领域命名，例如 minigame 的 `GameApp/WorldApp`；框架只提供 `root.Runtime` 和 `service.Service`。
- 纯 active map 查询必须使用 `GetRoot(rootID)` 这类 `Get*` 命名；该接口只查内存 active root，
  不加载、不创建、不触发 hook。
- `Activate(ctx, rootID)` 是生命周期入口，不是 getter。首次激活执行
  `LoadOrNew -> OnLoad -> OnActivate -> active`；同 root 并发首次激活必须合并。
- 已 active 后再次 `Activate` 只执行 `OnActivate`，用于登录、重连或重新激活事件；不能重复执行
  `LoadOrNew` 或 `OnLoad`。
- 首次激活失败不能写入 active map，后续调用必须可以重试。

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
- [Feature 目录模板](../../templates/root-service-feature.md)
