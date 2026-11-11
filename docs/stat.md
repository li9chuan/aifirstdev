# Typed Stat：结构体事件名、本地 CSV 与可选转发

本页描述当前源码能力，随下一根版本发布。业务项目以所锁定版本为准。
完整可执行接线见 [User.Stat 示例测试](../aifd-runtime/user/stat_example_test.go)。

## 职责与记录路径

```text
业务 u.Stat(payload)
  -> 去掉一层指针，取具名 struct 的类型名作为事件名
  -> 首次使用校验并缓存事件字段布局
  -> 同步调用业务公共头构造函数
  -> 公共头字段 + 事件字段，编码一次
  -> 本进程 Stat 文件追加
  -> 可选有界队列 -> 项目转发函数 -> 项目接收端保存原文
```

`aifd-log` 拥有事件类型与名称冲突检查、列校验、布局缓存、CSV 编码、本地写入与转发队列。
`aifd-runtime` 只接入 User 公共头回调；事件不依赖 feature 清单或 RPC 注册。
业务拥有公共头的全部字段、值、顺序和时间格式，以及事件含义、版本、接收服务、补录与
下游转换。Stat 不要求 observe-agent 或聚合服务。异步队列只接收不可变字符串。

## 事件结构体即事件定义

```go
type JobFinished struct {
    JobType    string `stat:"job_type"`
    Result     string `stat:"result"`
    DurationMS int64  `stat:"duration_ms"`
}
```

事件无需实现接口或方法。`User.Stat(payload any)` 与
`RecordStat(payload any, buildHeader func(eventName string) any)` 接受具名 struct 或
其非 nil 指针；启用 Stat 时，非 struct、匿名 struct、nil 和多层指针在使用时返回记录错误。
公共头仍允许匿名 struct。业务不传事件名或定义对象，不提供 feature 清单，也无需入口注册。

事件名直接采用去掉指针后的 `reflect.Type.Name()`，因此 `JobFinished{}` 和
`&JobFinished{}` 都使用 `JobFinished`。保留原始大小写，不转换下划线，不拼接包路径，
也不读取载荷字段。类型别名仍对应原类型和原事件名，不会产生新事件。
框架不绑定具体分析平台的命名限制，下游接入方负责检查所用类型名是否满足平台要求。

首次使用校验并缓存字段布局，后续同一类型的值和指针复用缓存。一次 logger 生命周期内，
同一事件名只能属于一个 Go 类型；不同包或不同作用域中的同名类型也会冲突。
冲突在写入或转发前拒绝，并报告双方类型及包路径，已接受类型仍可继续记录。
并发首次使用的发布和冲突检查受锁保护，公共头回调在锁外调用。无效事件字段不会占用名称。

结构体名是数据契约的一部分：重命名结构体会改变事件名，需要同步更新消费端的映射、查询和报表。

字段按 struct 声明顺序输出。导出字段须有显式 `stat` 标签，`stat:"-"` 忽略；不展开嵌套
对象。支持字符串、布尔、整数、浮点与 `time.Time`；后者仍按 RFC3339Nano 编码并保留
业务时区。需要其他时间格式时，由业务格式化为 string。零值保留列；重复列名、容器等
非法字段在首次使用时拒绝。公共头和当前事件之间的列冲突也在写入前检查。
CSV 使用标准转义，不加普通日志前缀或列名行，至少需要一列。

## 默认应用公共头与 User 接线

应用默认公共头采用三列，时间精确到秒。这是业务示例约定，不是框架强制字段：

```go
type AppStatHeader struct {
    Time      string `stat:"time"`
    EventName string `stat:"event_name"`
    UID       int64  `stat:"uid"`
}
```

进程已有日志初始化处调用一次 `InitDefault`，再创建 runtime 和启动生产者：

```go
closeLog, err := logging.InitDefault(logging.Config{
    LocalPath: "run/logs/service.log",
    Stat: &logging.StatConfig{
        LocalPath: "run/logs/stat.log",
        // Forward: applicationForward,
    },
})
if err != nil { return err }
defer closeLog() // 外层入口先停业务任务和 RPC，再返回

modules := user.NewManager[*pb.DBUser](jobsFeature, loginFeature)
users := user.NewRuntime(reg, store, modules, user.WithStatHeader(
    func(u *user.User[*pb.DBUser], eventName string) AppStatHeader {
        return AppStatHeader{
            Time:      time.Now().UTC().Format("2006-01-02 15:04:05"),
            EventName: eventName,
            UID:       u.UID(),
        }
    },
))
if err := users.RegisterRPC(); err != nil { return err }

// 业务确认结果后记录：
u.Stat(JobFinished{JobType: "import", Result: "success", DurationMS: 235})
```

示例沿用 UTC 取时，格式化后的字符串不含时区标记；其他时区由业务显式决定。
User 在 OnLoad 前继承头回调，每次打点读取当前值。读取 User 数据遵守业务锁约定，在业务
提交后释放会与回调冲突的锁再记录，不在 User 写锁内递归 Read。

`RegisterRPC` 仍负责业务路由；若配置 User 头回调，它检查回调与 User 类型、头字段及
nil builder，不枚举事件或提前检查事件冲突。独立服务任务直接调用
`logging.RecordStat(payload, buildHeader)`，按需使用 `ValidateStatHeader[H]()` 提前校验
头字段；两者都不需要事件注册。包装内部 feature 的模块也无需转交事件定义。

事件非法、身份冲突、头回调异常、缺失头配置或写入失败会计数并限频记录普通错误日志。
`User.Stat` 不返回业务错误，不回滚业务；`RecordStat` 同时返回错误供调用方处理。
未触发过的事件不会预先检查，迁移后不能依赖原先的启动事件校验。

默认公共头产生的原始记录例如：

```csv
2026-11-11 08:00:00,JobFinished,1001,import,success,235
```

框架不提供事件版本元数据，默认头也不输出任何版本列。需要按版本消费时，由业务显式加入
版本字段并维护转换规则，或使用新的结构体类型名，例如 `JobFinishedV2`；后者对应新的
事件名 `JobFinishedV2`。公共头与事件字段的演进仍由业务管理。

## 接口迁移

旧的 `DefineStat`、`RegisterStatEvents`、`StatEventProvider` 和 feature 的 `StatEvents`
清单已移除，不保留 wrapper。进一步移除 `StatSchema`、`StatEvent` 和事件的身份声明方法；
`RecordStat` 与 `User.Stat` 接收 `any` 并在运行时校验具名 struct。公共头回调的事件参数改为
`eventName string`，直接赋给业务事件名列。原先显式命名为 `job_finished` 的事件需要把消费端
映射迁移到实际结构体名 `JobFinished`，日志内容不会继续保留旧名。旧 `RegisterStatHeader` 改为纯校验
`ValidateStatHeader[H]()`，与事件组合有关的校验移到首次使用。每次 `InitDefault` 会新建
缓存与计数；仍应在启动时初始化，避免运行中重设 logger 并关闭仍有生产者使用的旧实例。

## 可选转发与补录

`StatConfig.Forward` 是 `func(context.Context, string) error`。字符串包含一条完整
CSV 记录及结尾换行，字段内部也可能有合法换行。HTTP/RPC、地址、鉴权和接收文件归项目。

```go
cfg.Stat.Forward = func(ctx context.Context, record string) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        receiverURL, strings.NewReader(record))
    if err != nil { return err }
    req.Header.Set("Content-Type", "text/csv; charset=utf-8")
    resp, err := client.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("stat receiver returned %s", resp.Status)
    }
    _, err = io.Copy(io.Discard, resp.Body)
    return err
}
```

本地受锁保护直接追加，成功后非阻塞地尝试入转发队列；远端 I/O 在锁外独立 worker 执行。
队列满只丢弃远端副本，失败或超时不回退本地记录、不自动重试。本地写入失败则不发送。
接收端原样追加，不重新生成公共头、时间或再次转发。补录工具使用 CSV parser 识别逻辑
记录，不按物理换行拆分；范围、进度和重复处理由项目维护。需要去重时可以在业务头加入
一次生成的记录 ID。框架不维护持久投递游标、确认或 exactly-once 承诺。

## 配置与生命周期

- `Config.Stat == nil` 禁用 typed Stat，不校验事件或调用公共头回调；启用时 `LocalPath` 必填。
- 普通日志与 Stat 复用同一 Lumberjack 文件组件，但使用不同文件和各自的编码。
  Stat 文件命名统一使用 `stat.log`，内容仍为原始 CSV；初始化失败保留原默认 logger。
- 两者默认按本地自然日和 1 GiB 切分；`Config.Rotation` 与 `StatConfig.Rotation` 独立配置。
  重启检查非空活动文件的修改日期，同日追加、跨日先切分；归档命名与保留配置见 [日志文件](../aifd-log/README.md#文件切分)。
- 队列、单次超时和关闭排空超时零值默认 1024 条、2 秒和 5 秒，负值拒绝。
- `FileConfig.Stat` 支持本地路径和数值选项，项目在 `ToConfig` 后注入转发函数。
- `GetStatCounters()` 返回本地成功、记录错误、转发成功/错误与远端副本丢弃数。
- 停服先停生产者，再调用 `closeLog()`：拒绝新追加、关闭本地文件、限时排空远端队列。
  达到期限取消当前请求，丢弃待发副本并返回超时；本地原始记录保留。
- 转发函数必须遵守 context，不能自行派生脱离它的后台投递。
- 文件 Write 成功不等于逐条 fsync，Stat 与业务存储不组成事务。

`aifd-log` 不依赖 observe；typed CSV 不自动发送给普通日志的 `SecondaryHandler`。
旧字段式 `Stat/Statf` 和全局默认 Sink 入口已移除，观测事件由宿主显式组合 `Event` 与
`Sink.Write`。`NewLocalFirstLogger` 只构造普通日志实例；
typed 配置由 `InitDefault` 管理，给实例构造函数传非 nil Stat 配置会返回错误。

## 验证

```sh
go test ./aifd-log/... ./aifd-observe/... ./aifd-runtime/...
go test -race ./aifd-log/... ./aifd-runtime/user ./aifd-runtime/service
```

race 需要支持 cgo 的 C 编译器。测试覆盖结构体名称、值/指针/别名的缓存复用、跨包同名冲突与并发首次使用、非法载荷、业务公共头、CSV、HTTP 内容一致、
快照、远端缺席/超时/异常/队列满、本地失败、并发追加和限时关闭。
