# 未发布变更

## 日志拆包与轮转

- 将日志统一入口迁到 `aifd-log`，包名仍为 `logging`，不新建 Go module；runtime、proxy、RDB 和示例迁移导入路径。
- 普通日志默认从 JSON 改为 `slog` 文本；`stat.log` 保持原始 CSV。共享 Lumberjack v2.2.1，默认 1 GiB 和本地自然日切分，补齐跨日启动、写入前检查与关闭保护。
- 相对日志路径在初始化时固定，避免切换工作目录后轮转或关闭漂移；Lumberjack 改为带来源与许可证的内部副本，仅修正关闭生命周期，等待归档清理/压缩完成并退出 worker，覆盖反复创建关闭和初始化失败重试。
- observe 专属转换移到 `aifd-observe/adapters/logging.NewHandler`；`Config.Observe` 改为 `SecondaryHandler`。移除旧字段式 `Stat/Statf`、全局默认 Sink 和旧日志包路径，升级方需适配；观测 envelope 仍可通过 `Sink.Write` 显式输出。
- observe-agent 支持新文本格式与历史 JSON，CSV 作为原文查询。文件命名、保留配置及迁移见 [日志说明](../../aifd-log/README.md)。

## Typed Stat

- 事件名直接取具名 struct 的类型名，保留大小写；`User.Stat` 与 `RecordStat` 接收 struct 或非 nil 指针，首次使用时校验并缓存布局，无需事件声明方法、feature 绑定或入口注册。
- 移除 `StatSchema`、`StatEvent` 与旧事件定义、清单注册接口，不保留 wrapper；公共头回调改为接收 `eventName string`，版本归业务管理。旧显式名称如 `job_finished` 需随升级映射到实际类型名 `JobFinished`，重命名类型也会改变日志事件名。
- 值、指针和类型别名共享布局；不同 Go 类型的同名事件（包括跨包）在写入前拒绝，并发首次使用只接受一个类型。非法事件不占用名称；User 启动仍只检查公共头自身的字段和回调配置。
- 默认应用公共头示例收敛为 `Time、EventName、UID`，时间按 `2006-01-02 15:04:05` 格式化为字符串；框架不强制这些列，也不自动写入版本。
- `logging.Config.Stat` 提供独立本地 CSV 和可选有界异步转发；两端使用同一成品记录，接收服务及补录留在业务。
- 增加原始记录计数、失败诊断、限时关闭、配置文件选项与运行示例。
- 用法与验证范围见 [Typed Stat](../stat.md)，本次不修改业务项目依赖、协议或生成物。

已发布原生 HTTPS/WSS、自动证书和限流处理边界见 [v0.6.0 发布说明](v0.6.0.md)。

业务项目以所锁定的根 module 版本为准；源码存在、测试通过与已发布是不同状态。
