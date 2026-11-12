# aifd-log

本页对应未发布源码；业务项目以所锁定根版本的 API 与升级说明为准。

进程内的统一日志入口，包名为 `logging`，与框架共用根 Go module 和版本。
它使用标准库和 [Lumberjack v2.2.1 内部副本](internal/lumberjack/UPSTREAM.md)，不依赖
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
框架固定字段。事件名直接取具名 struct 的类型名，例如 `JobFinished`；值与非 nil 指针
共用名称及布局，不需要声明方法、feature 绑定或事件注册。不同类型使用同名事件时在写入前拒绝。
`User.Stat(payload)` 和可选转发见 [完整 Stat 接线](../docs/stat.md)
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

Lumberjack 负责大小切分、归档命名、保留清理及压缩；内部副本修补清理协程生命周期、归档时区
解析、后台错误出口及 CSV 失败追加回退，保留上游来源与许可证。本包补齐自然日调度、启动检查、
写入前日期检查和关闭保护，具体差异见 [内部副本说明](internal/lumberjack/UPSTREAM.md)。
相对路径在文件组件初始化时解析为绝对路径，后续切换工作目录不改变写入、切分、清理和关闭位置。
切分与防覆盖检查共用含时区偏移的毫秒精度归档名，切分前检查名称是否已占用，避免快速连续切分
覆盖归档。午夜归档非空活动文件；没有新记录的空文件不会逐日生成归档。
重启时根据非空活动文件的修改日期检查是否需要先切分，同日则继续追加。
日切失败会写 stderr 并重试，跨日记录在切分成功前不会追加或转发。

活动文件名保持不变，归档使用切分发生时的时间戳与数字时区偏移，时间戳不是记录所属日期。
以下示例使用 UTC+8；配置 `UTC: true` 时偏移为 `+0000`：

```text
game.log
game-2026-11-12T00-00-00.123+0800.log
stat.log
stat-2026-11-12T00-00-00.124+0800.log
stat-2026-11-11T18-30-00.000+0800.log.gz
```

同一天超过上限可产生多份归档。单条编码后的记录超过大小上限时返回错误，不拆分该条记录。
文件须由单个进程、单个 logger 独占写入；多进程使用不同目录或路径。日志格式不由后缀决定。
`MaxBackups` 和 `MaxAgeDays` 是归档保留策略，设定前应考虑项目补录所需时间；清理由文件组件后台执行。
`MaxAgeDays` 的一天按 24 小时计算，归档时间按名称中的偏移还原为真实时刻，和自然日切分是两个不同规则。
夏令时回拨期间的 `01:30 -0400` 与 `01:30 -0500` 会生成不同名称，排序和清理也能区分这两个时刻。

兼容读取旧的不带偏移归档名，无需批量改名；旧名仍依赖原有 UTC/本地时区配置。
旧名落在回拨产生的重复时间段时，按较晚的候选时刻排序与计算保留期限，避免按年龄提前删除。
这种估计可能比真实归档时间多保留一个回拨时长；`MaxBackups` 的数量限制仍独立生效。
新旧格式的 `.log` 与 `.log.gz` 均参与保留和压缩管理，日志内容及 CSV 日期字段格式不变。

## CSV 恢复与文件错误

Stat 启动时流式扫描已有活动文件，接受不同列数和引号内换行。框架写出的完整记录必须以换行
结束；语法损坏或缺少结尾换行的尾部，先原样保存并同步到同目录的 `stat.log.corrupt-*` 文件，
再截断活动文件至最后一条完整记录。正常前缀和原修改时间保留，跨日重启仍会先归档。
读取或隔离失败会拒绝初始化，不把读取故障当成可丢弃的尾部。恢复不会转发历史记录。

隔离文件不参与自动压缩、保留清理或补录；检查、修复和删除由项目安排。若损坏发生在文件中段，
从最后完整记录开始的剩余字节一起隔离，框架不猜测后面的记录是否可信。

运行期 Stat 追加发生短写或错误时，回退这条未接受记录的字节和文件偏移，不计本地成功，也不转发。
重开已有 Stat 文件时使用可截断的写句柄并定位末尾，保证 Windows 下也能回退；重开失败直接报错，保留原文件。
回退失败后拒绝后续写入与轮转，关闭报告错误；重启后通过上述恢复流程隔离坏尾部。
普通文本日志不应用 CSV 扫描与恢复。启动扫描的读取量随活动文件大小增长。

归档清理/压缩失败即时写 stderr，首次后台失败保留到 `Close()` 返回；后续重试成功也不抹去
已报告的失败。记录仍可继续写入，后台错误不会递归写回出错的日志文件。

## 文件配置

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

先成功初始化日志，再启动业务生产者。初始化前包级 logger 默认丢弃输出。
停服先停业务生产者，再 `closeLog()`：停止日切 worker、同步并关闭活动文件，完成待处理的归档
清理/压缩并等待其 worker 退出，并限时排空可选 Stat 转发队列。转发排空超时不限制本地文件
关闭和压缩的耗时。关闭可重复调用；关闭后写入不会重新打开文件。初始化失败也回收已创建的 worker。
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

Typed Stat 直接接收具名 struct 或非 nil struct 指针，移除事件身份接口及元数据结构，
公共头回调改为接收 `eventName string`。不转换事件名的大小写或分词方式；结构体重命名会
改变事件名，升级时需同步消费端映射。字段布局和跨包同名冲突在首次使用时校验，User 启动
仍检查公共头自身的字段。默认应用头示例为 `Time、EventName、UID`，时间由业务格式化为
`2006-01-02 15:04:05`；版本字段和演进由业务管理。

```sh
go test ./aifd-log/... ./aifd-observe/... ./aifd-runtime/...
go test -race ./aifd-log/... ./aifd-observe/adapters/logging ./aifd-observe/agent ./aifd-runtime/user ./aifd-runtime/service
```
