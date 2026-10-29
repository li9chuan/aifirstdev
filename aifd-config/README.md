# aifd-config

## 定位

`aifd-config` 是 aiFirstDev 的强类型配置 bundle 生命周期层。它负责加载、校验、
prepare-then-commit reload、轻量 manifest 审计和表级 hash diff。

Luban 生成的 cfgtable、业务组合出的派生索引和业务读取 API 仍由业务项目拥有。
`aifd-config` 只持有 `*config.Bundle[T]` 指针，不把真实业务表全量映射成中立
`map[string]any` 或通用 row/table 中间结构。

## 负责

- `Bundle[T]`、`Manifest`、`Loader[T]`、`Validator[T]`、`Issue`、`ReloadResult` 契约。
- `Manager[T]` 的 prepare-then-commit reload 行为。
- 基于 manifest version、data hash、table hash 和 row count 的轻量 change-set。
- 用于测试、工具和项目接入的 memory、JSON、Luban bundle builder wrapper 和 Viper adapter。
- `adapters/luban/bin` 提供 Luban Go-bin 生成代码所需的边界安全 `ByteBuf`，不理解业务表结构。

## 不负责

- 真实 Luban 表定义、Excel 文件或生成的 cfgtable 包。
- cfgtable 到通用 row/table 中间结构的全量映射。
- feature 专属索引、派生缓存、常量、业务 reader 或校验规则。
- 运行期玩法状态、持久化模型或协议 schema。

## 业务项目如何使用

业务项目构建自己的强类型配置对象，例如 `*cfg.Tables`、`*AppConfigBundle` 或
`ServiceFileConfig`。如果 cfgtable 和派生索引必须强一致，它们应一起放入同一个
`Bundle.Data`，由 loader/builder 在发布前构建完成。

Loader/Builder 负责构建、normalize、remap、补默认值和派生索引。Validator 和 prepare hook
只读检查 `Bundle[T]`，可以构建模块私有视图或缓存，但不能修改将要发布的 bundle。

`Manifest` 只用于控制面、审计、版本、hash 和影响面报告。它可以只覆盖当前项目关心的表，
不要求每新增一张 Luban 表都同步写一层 `aifd-config` 映射。

服务入口也可以使用 `adapters/viper` 将 TOML、YAML、JSON、默认值和环境变量覆盖解码为
typed config，再显式组装 `proxy`、`rpc`、`rdb`、`observe` 和 `runtime` 的 Options。底层框架包不
直接读取配置文件，也不直接依赖 Viper。

### API 入口与 reload 结果

契约导入 `aifd-config/contracts`（通常别名 `config`），Manager 位于 `aifd-config/runtime`；
根目录不是另一个 Go facade 包。以 loader 和 validators 构造 `runtime.NewManager`，调用 `Reload(ctx)` 后检查两层结果：

- `err != nil`：加载或 prepare 失败。
- `err == nil` 但 `result.Applied == false`：可能是 error 级 validator issue 拒绝，从 `result.Issues` 取原因；不能仅凭 nil error 宣布配置已应用。

`Current()` 返回已提交 bundle 与 loaded 标记。宿主串行触发 Reload，框架不协调并发加载的版本顺序。
Prepare 应完成可失败工作，Apply 是提交后执行的无错误返回回调，不提供 panic 回滚或多组件数据库事务。
实现见 [Manager](runtime/manager.go) 和 [Hook](runtime/hooks.go)。真正的 Luban 生成命令在 [aifd-tools](../aifd-tools/README.md)；`tools/luban` 当前只是包占位。

## 更新边界

小版本可以增加 validator、adapter 或 manifest 元数据，但 reload 语义必须保持“失败时不发布”。
`Bundle.Data` 不由框架 clone；loader 返回后调用方必须将其视为不可变对象。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [配置工作流](../aifd-rules/skills/config-table-workflow/SKILL.md)
- [Viper 配置适配](adapters/viper/README.md)
