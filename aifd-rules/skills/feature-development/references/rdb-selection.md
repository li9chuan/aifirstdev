# 业务持久化选型

新增或调整业务存储时，先明确状态由谁写入、是否允许延迟保存，以及原子操作的范围。优先复用现有 Saver、Store 或 adapter。

| 场景 | 优先选择 |
|---|---|
| 单一 owner 写入的 protobuf 实体，只需保存最终状态，允许延迟合并 | RDB `SetSaver` / `HashSaver` |
| 即时唯一性、原子计数或多个写入方协调 | 已有 Redis Store 或适用的原子命令，如 `SETNX`、`INCR` |
| 需要 ZSet、List 等原生结构及其访问方式 | 对应的 Redis adapter |
| 需要原子比较并更新多个值，现有接口或原子命令无法表达 | 在拥有该存储的 adapter 中使用 Lua |

采用原生 Redis/Lua 且原因不明显时，在方案或关键注释中说明具体需求；已有说明无需重复。

## 使用边界

- 普通业务使用 `Save`，由 RDB 后台合并写入；成功表示意图已入队。停服时由宿主统一调用 `Close` 并处理返回错误。
- `Flush` 用于测试、迁移等需要主动排空队列的场景，普通业务通常不调用；它不提供 CAS、跨实体事务或修改历史。
- 同一正文（SET key 或 HSET key+field）使用一致的写入路径，避免延迟保存覆盖原生 Redis/Lua 更新；独立索引可使用原生结构。
- 已有存储随相关功能改动评估，保留原有一致性语义。

## 审查重点

关注是否在业务中重复实现 RDB 已有的保存、编解码、合并或重试。`.lua`、`Eval`、`NewScript` 和直接 SET/HSET 只作定位线索，按用途和一致性需求判断。

## 用法与实现

- [RDB 接入、TTL 和失败恢复](../../../../aifd-rdb/README.md)
- [账号 Redis Store：即时映射与 ID 分配](../../../../aifd-account/redis_store.go)
