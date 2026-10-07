# AIFD Service / Root Runtime

## 定位

AIFD runtime 固定两类通用能力：

- `runtime/root`：按 ID 管理业务根文档、激活、锁、快照、持久化和 root module。
- `runtime/service`：管理服务级 module 的恢复、RPC 注册、启动和关闭顺序。

它们是框架能力，不是业务服务名。业务应用应按领域命名自己的服务：游戏示例可以叫
`game/world`，Wails 工具可以叫 `workspace/index/jobs`。

## Root Runtime

`root.Runtime[T]` 负责当前业务根文档域：

- 根据 root ID 激活、加载或创建业务根文档。
- 管理 active root、并发首次激活合并和重复激活 hook。
- 为 root module 提供 `OnLoad`、`OnActivate`、`RegisterRPC`。
- 通过 `root.Root[T]` 封装锁、快照和 `aifd-rdb` 保存能力。

root runtime 不应拥有跨 root 关系真相源、全局排行榜、全服邮件投递或共享索引。

## Service Lifecycle

`service.Service` 负责服务级生命周期：

- 在开放写入、tick 或外部事件前执行恢复。
- 注册服务级 RPC。
- 启动和关闭 worker、tick、订阅或异步任务。
- 承载跨 root 的索引、聚合、投递、关系、广播或任务 module。

service lifecycle 可以和 root runtime 同进程运行，也可以拆成独立进程，通过 `aifd-rpc`、
NATS、Redis 等 adapter 连接。框架只提供可拆分边界，不要求 k8s 或复杂服务编排。

## 插件归属

mail、friend、guild、ranking 这类可选能力不进入 `aifd-runtime/root` 或
`aifd-runtime/service` 核心目录。推荐放在独立插件包或业务项目插件包：

- `aifd-plugins/<name>/service`：跨 root 真相源、索引、投递、关系或任务。
- `aifd-plugins/<name>/root`：root 侧视图、缓存、红点或在线推送。
- `aifd-plugins/<name>/proto` 和 `generated`：插件自己的协议和生成物。

插件由应用入口显式组合；import 不能自动注册 route、启动 worker 或创建运行期连接。

## 部署形态

AIFD 使用 logical services，而不是强制 physical microservices：

- `all-in-one`：game、world、proxy、account、adapter 同进程，适合开发、单服和本地工具。
- `split`：game、world、proxy 拆成多个进程，使用 RPC bus 或共享存储连接。
- `hybrid`：少数高压 service module 独立，其余模块同进程。

业务模块只依赖 root/service 契约，不依赖部署方式。部署形态变化不应要求改业务 feature 的状态归属。

## 非游戏示例

文档管理工具可以按真实领域命名：

- `workspace`：当前用户文档、偏好和历史查询，可组合 root runtime。
- `index`：共享文档索引和检索缓存，可组合 service lifecycle。
- `jobs`：导入、导出和索引任务，可按状态真相源选择 root module 或 service module。

Wails 前端只调用 Go binding 的业务门面，例如 `ImportDocument(path)`、`ListDocuments()`，不直接接触
root/service runtime 细节。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Runtime 能力与用法](../aifd-runtime/README.md)
- [Runtime 模块开发](../aifd-rules/skills/runtime-module-development/SKILL.md)
- [Root / Service Feature 模板](../aifd-rules/templates/root-service-feature.md)
