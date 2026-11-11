# Typed Stat：本地 CSV 与可选转发

本页描述当前源码新增的能力，随下一根版本发布。已发布依赖是否可用，以锁定版本为准。
完整可执行接线见 [User.Stat 示例测试](../aifd-runtime/user/stat_example_test.go)。

## 职责与记录路径

```text
业务 u.Stat(payload)
  -> 同步调用业务公共头构造函数
  -> 公共头字段 + 事件字段，编码一次
  -> 本进程 Stat 文件追加
  -> 可选有界队列 -> 项目转发函数 -> 项目接收端保存原文
```

`aifd-log` 拥有事件定义、列校验、CSV 编码、本地写入与转发队列。
`aifd-runtime` 沿已有模块收集事件定义，接入 User 公共头回调。业务拥有公共头的全部
字段、值和顺序，事件含义与版本、接收服务、补录及下游转换。Stat 不要求 observe-agent
或聚合服务。异步队列只有不可变字符串，不保存 User 或业务对象引用。

## 结构与 feature 声明

```go
type AppHeader struct {
    HeaderVersion int       `stat:"header_version"`
    EventName     string    `stat:"event_name"`
    EventVersion  int       `stat:"event_version"`
    Time          time.Time `stat:"time"`
    UID           int64     `stat:"uid"`
}

type JobFinished struct {
    JobType    string `stat:"job_type"`
    Result     string `stat:"result"`
    DurationMS int64  `stat:"duration_ms"`
}

func (*JobsFeature) StatEvents() []logging.StatSchema {
    return []logging.StatSchema{
        logging.DefineStat[JobFinished]("job_finished", 1),
    }
}
```

框架不强制 UID、时间、版本等公共列，不引入 HeaderName 或 StatMeta。
`StatSchema.Name/Version` 来自 feature 声明，框架复制并传递，不判断历史兼容性。
`DefineStat` 不注册、不做 I/O、不启动 goroutine；错误在启动注册时返回。

一个载荷类型只能绑定一个事件身份；相同定义重复出现是幂等的；不同类型复用同一
`(Name, Version)` 或同一类型绑定不同身份均拒绝。并存两版时分别定义载荷类型。
struct 和非 nil struct 指针归一为同一类型。

列序按字段声明顺序。导出字段必须有显式 `stat` 标签，`stat:"-"` 忽略；不推断其他
标签，不自动展开嵌套对象。支持字符串、布尔、整数、浮点及 `time.Time`，后者使用
RFC3339Nano，时区由业务提供。零值保留列，结构内重复列名、容器等在注册时拒绝。
User 模块的公共头与事件列冲突在启动时检查；服务级自由组合在写入前检查，其他头和事件
可以共存，不因从未组合过的同名字段互相限制。
CSV 使用标准转义，保留单个空字段记录，至少需要一列；不写普通日志前缀或列名行。

## 启动与 User 接线

在进程已有日志初始化处调用一次 `InitDefault`，先初始化，再注册模块、接流量。

```go
closeLog, err := logging.InitDefault(logging.Config{
    LocalPath: "run/logs/service.log",
    Stat: &logging.StatConfig{
        LocalPath: "run/logs/stat.log",
        // Forward: applicationForward, // 不配置即只写本地
    },
})
if err != nil { return err }
defer closeLog() // 外层入口先停业务任务和 RPC，再返回

modules := user.NewManager[*pb.DBUser](jobsFeature)
modules.Register(loginFeature) // 启动期追加，同样发现 StatEvents
users := user.NewRuntime(reg, store, modules, user.WithStatHeader(
    func(u *user.User[*pb.DBUser], event logging.StatSchema) AppHeader {
        return AppHeader{
            HeaderVersion: 1,
            EventName: event.Name, EventVersion: event.Version,
            Time: time.Now().UTC(), UID: u.UID(),
        }
    },
))
if err := users.RegisterRPC(); err != nil { return err }

// 业务确认结果后记录：
u.Stat(JobFinished{JobType: "import", Result: "success", DurationMS: 235})
```

`user.Manager.RegisterRPC` 和 `service.Service.RegisterRPC` 在业务路由前批量收集
`logging.StatEventProvider` 并检查冲突，无需向 `InitDefault` 传事件清单。
包装内部 feature 的模块显式转交已有列表的 `StatEvents()`，框架不反射遍历业务对象：

```go
func (m *Module) StatEvents() []logging.StatSchema {
    var events []logging.StatSchema
    for _, feature := range m.features {
        if provider, ok := feature.(logging.StatEventProvider); ok {
            events = append(events, provider.StatEvents()...)
        }
    }
    return events
}
```

仅支持启动期注册；不能运行中重调 `InitDefault` 后继续使用旧接线，因为重新初始化会
重置当前进程的定义与计数。头回调返回固定具体结构，每次读取当前值。读取 User 数据仍
遵守业务锁约定；在业务提交后释放会与回调冲突的锁再打点，不在 User 写锁内递归 `Read`。
异常、未注册载荷、缺失头回调和写入失败会计数并限频记录普通错误日志；`User.Stat`
不返回业务错误。服务级可用 `logging.RecordStat(payload, buildHeader)`，它同时返回错误。

本例原始记录如下。前三列是否固定、各历史版本的布局与转换均属于业务消费契约：

```csv
1,job_finished,1,2026-01-02T03:04:05Z,1001,import,success,235
```

公共头版本只在应用头中声明，事件版本只在 feature 定义中声明，两者独立演进。

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

- `Config.Stat == nil` 禁用 typed Stat，不调用公共头回调；启用时 `LocalPath` 必填。
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
go test ./aifd-log ./aifd-observe/... ./aifd-runtime/...
go test -race ./aifd-log ./aifd-runtime/user ./aifd-runtime/service
```

race 需要支持 cgo 的 C 编译器。测试覆盖自动注册与冲突、业务公共头、CSV、HTTP 内容一致、
快照、远端缺席/超时/异常/队列满、本地失败、并发追加和限时关闭。
