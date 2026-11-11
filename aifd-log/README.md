# aifd-log

本页对应未发布源码；业务项目以所锁定根版本的 API 与升级说明为准。

进程内的统一日志入口，包名为 `logging`，与框架共用根 Go module 和版本。
它只依赖标准库和 [Lumberjack v2](https://github.com/natefinch/lumberjack)，不依赖
observe、RPC、RDB 或 runtime；import 不启动 worker，也不连接日志服务。

## 普通日志与 Stat

```go
import "github.com/li9chuan/aifirstdev/aifd-log"

closeLog, err := logging.InitDefault(logging.Config{
    SourceService:  "game",
    SourceInstance: "game-1",
    LocalPath:      "run/logs/game-1/game.log",
    AlsoStdout:     true,
    Stat: &logging.StatConfig{
        LocalPath: "run/logs/game-1/stat.log",
    },
})
if err != nil { return err }
defer closeLog()

logging.Infof("service started port=%d", 8080)
logging.Info("job finished", logging.Int64("uid", 1001))
```

普通日志采用 `slog.TextHandler`，同一条记录一次 Write，消息中的换行和引号会转义：

```text
time=2026-11-11T15:20:30.000+08:00 level=INFO msg="job finished" source_service=game source_instance=game-1 uid=1001
```

Stat 使用业务公共头与事件 struct，写入原始 CSV，不添加普通日志前缀、JSON、列名行或
框架固定字段。feature 注册、`User.Stat(payload)` 和可选转发见 [完整 Stat 接线](../docs/stat.md)
与 [可执行示例](../aifd-runtime/user/stat_example_test.go)。仅普通日志需要独立实例时可用
`NewLocalFirstLogger`；typed Stat 配置由 `InitDefault` 管理。

## 文件切分

普通日志与 Stat 使用同一个文件组件，各自独立配置 `Rotation`，Stat 不继承普通日志的
显式选项。默认行为如下：

| 配置 | 默认行为 |
| --- | --- |
| `MaxSizeMB` | 0 表示 1024 MiB，即 1 GiB |
| `DisableDaily` | false，按操作系统本地自然日切分 |
| `UTC` | false；true 时日切和归档名称使用 UTC |
| `MaxBackups` | 0，不限制归档数量 |
| `MaxAgeDays` | 0，不限制归档保留天数 |
| `Compress` | false；true 时后台 gzip 压缩归档 |

Lumberjack 负责大小切分、归档命名、保留清理及压缩；本包只补齐自然日调度、启动检查、
写入前日期检查和关闭保护。切分前还检查上游毫秒精度归档名是否已占用，避免快速连续切分
覆盖归档。午夜归档非空活动文件；没有新记录的空文件不会逐日生成归档。
重启时根据非空活动文件的修改日期检查是否需要先切分，同日则继续追加。
日切失败会写 stderr 并重试，跨日记录在切分成功前不会追加或转发。

活动文件名保持不变，归档使用切分发生时的时间戳，时间戳不是记录所属日期：

```text
game.log
game-2026-11-12T00-00-00.123.log
stat.log
stat-2026-11-12T00-00-00.124.log
stat-2026-11-11T18-30-00.000.log.gz
```

同一天超过上限可产生多份归档。单条编码后的记录超过大小上限时返回错误，不拆分该条记录。
文件须由单个进程、单个 logger 独占写入；多进程使用不同目录或路径。日志格式不由后缀决定。
`MaxBackups` 和 `MaxAgeDays` 是归档保留策略，设定前应考虑项目补录所需时间；清理由上游后台执行。

文件配置通过 `FileConfig.ToConfig` 转换，环境变量键由 `FileConfigEnvKeys` 提供，例如：

```toml
[log]
level = "info"
dir = "./logs"
stdout = true

[log.rotation]
max_size_mb = 1024
# max_age_days = 7
# max_backups = 30
# compress = true

[log.stat]
local_path = "./logs/stat.log"

[log.stat.rotation]
max_size_mb = 1024
```

## 生命周期与可选接入

先成功初始化日志，再注册 feature 并启动业务。初始化前包级 logger 默认丢弃输出。
停服先停业务生产者，再 `closeLog()`：停止本包日切 worker、同步并关闭活动文件，并限时
排空可选 Stat 转发队列。关闭可重复调用；关闭后写入不会重新打开文件。
运行期普通日志没有错误返回，文件写入失败输出到 stderr；typed Stat 记录错误还会计数。
Write 成功表示写入操作系统，不提供逐条 fsync，也不与业务存储组成事务。

`Config.SecondaryHandler` 可接入宿主提供的 `slog.Handler`；先写本地，成功后再调用该
handler，其错误不回退本地记录。调用是同步的，慢出口应由宿主显式组合有界异步 adapter。
handler 的关闭由宿主管理。observe 转换见 [可选 adapter](../aifd-observe/adapters/logging/handler.go)。
Stat 的 `Forward` 则有独立有界 worker，只接收同一份已编码 CSV；不要求 observe 服务存在。

## 升级与验证

此前的日志导入路径迁到本包，不保留旧路径 wrapper。`Config.Observe` 改为宿主显式
提供 `SecondaryHandler`。旧字段式 `Stat/Statf` 和默认 Sink 全局入口已移除；新打点用
typed Stat，需要观测 envelope 的已有调用方直接组合 `observe/contracts.Event` 和 `Sink.Write`。
这些是下一根版本的 breaking change，不要求业务项目立即升级。

```sh
go test ./aifd-log ./aifd-observe/... ./aifd-runtime/...
go test -race ./aifd-log ./aifd-observe/adapters/logging ./aifd-observe/agent ./aifd-runtime/user ./aifd-runtime/service
```
