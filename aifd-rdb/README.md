# aifd-rdb

## 定位

`aifd-rdb` 提供 protobuf-only 的 Set/HSet 持久化 mixin。业务对象通过匿名嵌入
`SetSaver[T]` 或 `HashSaver[T]` 获得 `Load`、`Save`、`SaveUnsafe`、`Delete`
等能力，不需要显式组合 store、codec 或 Redis adapter。

`Save`、`SaveUnsafe`、`Delete` 和 `DeleteUnsafe` 默认只进入 final-intent 延迟保存
队列；真正的 Redis 或 memory 写入由后台 worker、`Flush` 或 `Close` 完成。

SET/HSET 实际保存成功后，通过进程默认的 `aifd-observe/logging` 输出 `Rdb save Set`
或 `Rdb save HSet`。`size` 是已序列化 protobuf 的整数字节数，例如 `"size":1536`，
不换算 KB、不使用小数；空 protobuf 为 `0`。定位字段使用 `key_hash` / `field_hash`，
不输出原始 key 或 field。宿主使用现有 logging 初始化即可记录；失败或跳过的写入不输出成功保存日志。

## 负责

- `SetSaver`：一个 Redis key 保存一个 protobuf message。
- `HashSaver`：一个 Redis hash field 保存一个 protobuf message。
- `DB`：隐藏 memory 或 Redis 后端，并负责 WaitList final-intent 延迟合并写入。
- `DbGlobal` 心智：业务 saver 默认绑定包级 `DefaultDB`。
- TTL 语义：`WithTTL` 设置/续期；默认不设置 TTL；`KeepTTL` 仅对 Set 保留已有 TTL。
- failed final-intent journal：`Close` 遇到 Redis 连接类最终失败时，把未确认的最终意图写到本地文件。

## 不负责

- feature 专属 Redis key、field、生命周期维度或恢复策略。
- Player 模型、DBPlayer、World 状态或玩法模块。
- Redis 连接创建、关闭或重连。
- 严格 AOF、写前日志、跨 entity 事务顺序或中间 Redis 命令历史。
- ZSet、List、Pipeline、Incr 等 Redis 原生命令封装。

## 初始化

应用启动或 runtime 层创建 `DB`。新代码优先把 `DB` 显式传给 store 或 saver：

```go
db, err := rdb.NewMemoryDB(rdb.WithSaveDelay(20*time.Millisecond, 30*time.Second))
if err != nil {
	return err
}

var state rdb.SetSaver[*pb.PlayerState]
if err := state.BindDB(db, playerKey(uid), &pb.PlayerState{}); err != nil {
	return err
}
```

批量恢复一个精确 HSET key 时，可以使用 `LoadHash(ctx, db, key, newMessage)`。它用一次 HGETALL 解码为独立 protobuf 对象；任意字段损坏返回错误，不返回部分结果。调用方随后逐个绑定 HashSaver。该函数不扫描 keyspace，也不替代 owner 的首次加载合并。

`db.Persist(ctx, key)` 同步清除旧 key 的 TTL，适用于 owner 首次接管旧 Hash、但以后改为逐 field 过期的生命周期迁移。必须在 owner/Saver 锁外调用，先成功 Persist 再读取正文，在新对象发布和接受写入之前完成。HashSaver 默认 HSET 保留已有 key TTL，并不会自动清除旧 TTL。

需要让同一 entity 在短窗口内稳定合并时，可显式开启最小驻留窗口；默认关闭：

```go
db, err := rdb.NewMemoryDB(
	rdb.WithSaveDelay(20*time.Millisecond, 30*time.Second),
	rdb.WithSaveStayMin(5*time.Second),
)
```

使用 `DBFileConfig` 时对应字段为 `save_stay_min`。

兼容旧项目时，也可以设置包级默认 DB：

```go
db, err := rdb.NewMemoryDB(rdb.WithSaveDelay(20*time.Millisecond, 30*time.Second))
if err != nil {
	return err
}

db, err = rdb.NewRedisDB(
	redisClient,
	rdb.WithSaveDelay(20*time.Millisecond, 30*time.Second),
	rdb.WithFailureFileDir("run/rdb-failed"),
	rdb.WithErrorHandler(func(ctx context.Context, event rdb.ErrorEvent) {
		// 应用入口可在这里桥接到 local-first logger。
	}),
)
if err != nil {
	return err
}
rdb.SetDefaultDB(db)
```

如需启动时自动 replay 上次 `Close` 生成的 journal，显式开启：

```go
db, err := rdb.NewRedisDB(redisClient, rdb.WithStartupReplay())
```

如需接入运行指标，可传入 observe sink。默认指标不会携带原始 Redis key 或 field，只会上报
hash 后的定位字段，避免把 uid、账号或业务对象标识发到远端 observe：

```go
db, err := rdb.NewRedisDB(redisClient, rdb.WithObserveSink(observeSink))
```

本地排障确实需要原始 key/field 时，可显式打开：

```go
db, err := rdb.NewRedisDB(redisClient, rdb.WithObserveSink(localSink), rdb.WithObserveRawKeys())
```

关服或模块退出时强制落盘并停止 worker：

```go
if err := rdb.CloseDefaultDB(ctx); err != nil {
	return err
}
```

需要在低频锚点确认已落盘时，可显式 flush：

```go
if err := rdb.FlushDefaultDB(ctx); err != nil {
	return err
}
```

## 使用方式

业务对象匿名嵌入 saver，并在业务构造函数或恢复函数中绑定 key、field 和 pb：

```go
type PlayerState struct {
	rdb.HashSaver[*pb.PlayerState]
}

func NewPlayerState(uid int64) (*PlayerState, error) {
	state := &PlayerState{}
	err := state.Bind(playerKey(uid), playerField(uid), &pb.PlayerState{}, rdb.WithTTL(24*time.Hour))
	if err != nil {
		return nil, err
	}
	return state, nil
}
```

`BindDB` 会把显式传入的 `DB` 固定到 saver 内；`Bind` 则会把当时的 `DefaultDB()` 固定到
saver 内。后续 `SetDefaultDB` 只影响之后新 `Bind` 的 saver，不会迁移已绑定对象。运行期通常
保持单 DB 生命周期；如果确实需要切换 DB，应重新构造或重新绑定业务对象。

已持有对象锁时可调用 `SaveUnsafe`，它只入队，不做 Redis I/O：

```go
state.Lock()
state.DbProto.Score += 10
err := state.SaveUnsafe(ctx)
state.Unlock()
if err != nil {
	return err
}
```

不需要 unsafe 路径时可直接调用 `Save`：

```go
state.Lock()
state.DbProto.Score += 10
state.Unlock()
if err := state.Save(ctx); err != nil {
	return err
}
```

## TTL 和队列语义

- `WithTTL(d > 0)`：`SetSaver` 写入时设置 key TTL；`HashSaver` 正常写入时执行 `HSET key field` 后续期整个 hash key。
- 默认不传 TTL：`SetSaver` 无过期；`HashSaver` 只写 field，不改变 hash key TTL。
- `KeepTTL()`：`SetSaver` 保留已有 key TTL；`HashSaver` 与默认行为一致，不改变 hash key TTL。
- 同一个 Set key 或 Hash key+field 的重复保存会在 WaitList 中合并，只保留最终保存意图。
- `WithSaveStayMin(d)` 默认关闭；开启后普通 worker 只有在 entity 首次入队满 `d` 后才出队，期间继续合并同 entity 的最终意图。
- `Save -> Delete` 合并为最终删除；同一 saver 删除后不能再保存，`Delete -> Save` 只来自新绑定对象。
- worker 执行保存时才读取 saver 的最新 `DbProto` 并 marshal。
- `Flush` 和 `Close` 不受 `WithSaveDelay` 或 `WithSaveStayMin` 限制，会立即 drain 当前队列。
- Redis 连接类失败会进入 retry 槽；`Flush` 遇到这类失败会保留队列并返回 error。
- 权限、类型、marshal 等非重试错误保留失败 final intent，后台不会自动重试，也不进入 replay journal。`WaitSize` 包含这些未解决意图；读取或回传错误不会清除它们。
- 每次显式 `Flush` 对已有失败意图各尝试一次，然后 drain 普通队列；新的失败不会在同一次 Flush 内反复执行。修复存储后再次 Flush，或通过新的 Save/Delete 替代旧意图，均可解决故障。成功返回才表示本次 drain 没有未解决写入。
- `Close` 不受传入 context 取消影响；如果最终仍是连接类失败，会把未确认 final intents 写入 `source=close` journal 后停止 worker。
- `Close` 保留并持续返回非重试失败，不自动重试或将其写入 journal；调用方修复后可显式 Flush 已接纳的最终意图，即使 worker 已关闭。已关闭 DB 仍拒绝新的 Save/Delete。

## Journal 和 Replay

- journal 默认目录是 `run/rdb-failed`，可用 `WithFailureFileDir` 覆盖。
- journal 只记录 final intent，不记录严格命令历史；payload 使用 base64 写入本地 JSONL 文件，不进入普通日志。
- `WithStartupReplay` 只 replay `source=close` 的 `.jsonl` 文件；成功后改名 `.done`，不直接删除。
- replay 前 `.jsonl` 会改名为 `.replaying`；发现遗留 `.replaying` 时返回错误，要求人工确认。
- Hash journal replay 时，如果同一个 hash key 的记录全部是 save，会在所有 `HSET` 后按最大 positive TTL 执行一次 `EXPIRE`；混有 delete 时不续期。

## 边界

`HashSaver` 的 TTL 是整个 hash key 的 TTL，不是 field TTL。业务如果需要 field 级生命周期，
应把生命周期维度写进 hash key 或 field，并在结算、归档或清理流程中显式删除 field。

final-intent journal 不保证中间 `EXPIRE` 副作用，也不保证不同 entity 之间的事务顺序。
它只保证 Redis 故障窗口内每个 Set key 或 Hash field 的最终保存/删除意图可被恢复。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [项目级编码约束](../aifd-rules/skills/project-coding-constraints/SKILL.md)
- [项目与 Feature 代码评审](../aifd-rules/skills/runtime-feature-review/SKILL.md)
