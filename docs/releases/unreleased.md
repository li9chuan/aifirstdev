# 未发布变更

## 日志拆包与轮转

- 将日志统一入口迁到 `aifd-log`，包名仍为 `logging`，不新建 Go module；runtime、proxy、RDB 和示例迁移导入路径。
- 普通日志默认从 JSON 改为 `slog` 文本；`stat.log` 保持原始 CSV。共享 Lumberjack v2.2.1，默认 1 GiB 和本地自然日切分，补齐跨日启动、写入前检查与关闭保护。
- observe 专属转换移到 `aifd-observe/adapters/logging.NewHandler`；`Config.Observe` 改为 `SecondaryHandler`。移除旧字段式 `Stat/Statf`、全局默认 Sink 和旧日志包路径，升级方需适配；观测 envelope 仍可通过 `Sink.Write` 显式输出。
- observe-agent 支持新文本格式与历史 JSON，CSV 作为原文查询。文件命名、保留配置及迁移见 [日志说明](../../aifd-log/README.md)。

## Typed Stat

- 新增业务公共头与事件 struct、feature 自动注册及 `User.Stat(payload)`，不引入框架固定头或头版本。
- `logging.Config.Stat` 提供独立本地 CSV 和可选有界异步转发；两端使用同一成品记录，接收服务及补录留在业务。
- 增加原始记录计数、失败诊断、限时关闭、配置文件选项与运行示例。
- 用法与验证范围见 [Typed Stat](../stat.md)，本次不修改业务项目依赖、协议或生成物。

已发布原生 HTTPS/WSS、自动证书和限流处理边界见 [v0.6.0 发布说明](v0.6.0.md)。

业务项目以所锁定的根 module 版本为准；源码存在、测试通过与已发布是不同状态。
