# aifd-runtime

## 定位

`aifd-runtime` 把框架基础设施组合成业务生命周期边界。框架公共语言只使用
`root runtime` 和 `service lifecycle`；游戏、玩家、世界等词汇属于业务项目或示例，不作为
runtime 公共 API。

## 负责

- `root.Runtime[T]`：管理 active roots、root 根文档、root module 顺序和业务 RPC 注册。
- `root.Root[T]`：封装业务根 proto 的锁、快照和 `aifd-rdb` 保存能力。
- `service.Service`：管理服务级 module 的恢复、RPC 注册、启动和关闭顺序。
- 基于顶层 `rpc.Registry` 的业务 route 注册入口。
- config hook、lifecycle runner 等靠近服务组合的通用模式。
- `entrypoint.NewHTTPServer`：为公开 HTTP 入口提供显式、受限的连接超时和请求头上限。

## 不负责

- inventory、arena、guild war、event 等具体业务 feature。
- 业务 `.proto` 文件、Luban 表、生成的 pb 或生成的 cfgtable。
- 底层 RPC transport、rdb store 后端实现、proxy session、robot manager 或 observability sink。
- 为旧实验 API 保留 `game/player/world` wrapper。

## 业务项目如何使用

业务项目在此 runtime 之上构建 service host 和 feature module。Feature 代码应留在业务项目中，
通常放在 `features/`，并通过 runtime 公开扩展点接入，不直接修改基础设施内部实现。

`root runtime` 表达当前拥有者或当前分片内可以独立决策的数据域。游戏里可以是当前玩家根文档，
工具里可以是当前用户、工作区或分析会话。业务根文档由业务项目自己的 protobuf 定义，例如
`pbmsg.DBPlayer`；runtime 只通过泛型 `root.Store[T]` 和 `root.Root[T]` 组合 `aifd-rdb`
的 `SetSaver`，不理解具体字段或玩法语义。

`service lifecycle` 表达服务级恢复、启动、关闭顺序，也可承载跨多个 root runtime 的共享状态和全局流程。
邮件投递、好友关系、排行榜、广播、共享任务和全局索引应优先落在插件或业务项目自己的 service module。
Service 是逻辑生命周期边界，可以同进程运行，也可以拆进独立进程；runtime 不要求 k8s 或复杂服务编排。

业务 route 使用 `rpc.Client`、`rpc.Server`、`rpc.Admin`、`rpc.Stream` 注册；真实 Bus 创建、
adapter 选择和 runner 启动由服务入口负责。

## 更新边界

当前仍处于新框架设计阶段，runtime API 优先破坏式收敛到最小业务门面，不保留历史兼容层。
