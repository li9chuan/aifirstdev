# aifd-tools

## 定位

`aifd-tools` 提供 `aifd` CLI，用来维护 AI/开发者本地环境、安装和检查工具链、
执行 proto/config 生成命令，并管理本地 Redis 开发服务。

## 负责

- 保留 `aifd update` 占位入口；当前只输出计划信息，不检查远端版本，也不执行框架升级。
- `aifd tools doctor/path/install` 的工具链安装、定位、版本诊断和路径说明。
- `aifd proto gen` 对 `protoc` 的真实调用。
- `aifd config gen` 对 Luban 的真实调用。
- `aifd dev up/down/status` 管理本地 `redis-server` 开发态进程。
- `aifd.lock` 中 `tools.<name>` 版本的读取；框架版本统一由业务项目的 `go.mod/go.sum` 管理。

## 不负责

- 提交 `protoc`、Luban、Redis、Wails、NSIS 的二进制文件。
- 调用 winget、choco、brew、apt 等系统包管理器。
- 在 Windows 上伪装安装官方 `redis-server.exe`。
- 隐藏的业务 migration 或 feature 专属代码变更。
- 真实 `.proto` 文件、Luban 表、生成的 pb 或生成的 cfgtable。
- 应由框架模块或业务服务拥有的 runtime 行为。

## 业务项目如何使用

业务项目在项目根目录放置 `aifd.lock`，可选放置本地工具到：

```text
.aifd/tools/<os>-<arch>/<tool>/<version>/
  bin/<exe>
  include/...
  install.json
```

旧版 `.aifd/tools/<os>-<arch>/<tool>/<version>/<exe>` 仍可被解析，便于兼容已有项目。

工具解析优先级固定为：

1. CLI flag，例如 `--protoc <path>`。
2. 环境变量，例如 `AIFD_PROTOC`、`AIFD_LUBAN`、`AIFD_REDIS_SERVER`、`AIFD_WAILS`、`AIFD_MAKENSIS`。
3. 项目本地 `.aifd/tools`。
4. 系统 `PATH`。

常用命令：

```text
aifd tools doctor
aifd tools path
aifd tools install
aifd tools install --tool protoc
aifd tools install --tool wails
aifd tools install --tool nsis
aifd proto gen --proto_path proto --go_out gen/go --include proto/a.proto
aifd config gen --conf luban.conf --out gen/cfg --target go
aifd dev up
aifd dev status
aifd dev down
```

如果 `proto gen` 或 `config gen` 不传显式参数，CLI 会尝试读取
`.aifd/proto.args` 或 `.aifd/config.args`，一行可写多个参数，空行和 `#` 注释会被忽略。
显式生成参数会整体替换文件中的参数，不做逐项合并。参数文件按空白分词，不解析 shell 引号；含空格路径用 CLI 参数传入。

`tools install` 默认安装 `protoc`、`protoc-gen-go` 和 Luban。Redis 只作为可诊断工具保留：
Windows 下默认显示 unsupported，显式安装会失败并提示使用 WSL、Memurai、Docker、PATH 或
`AIFD_REDIS_SERVER`。

Wails 3 是桌面示例专项工具，不进入默认安装集合。稳定工具名仍为 `wails`，实际安装的可执行文件为 `wails3`；`AIFD_WAILS` 和 `--wails` 也应指向 `wails3`。需要开发桌面示例时显式安装：

```text
aifd tools install --tool wails
```

NSIS 是 Windows 桌面安装包专项工具，也不进入默认安装集合。需要执行 Wails NSIS 打包时显式安装：

```text
aifd tools install --tool nsis
```

安装后可把项目本地 NSIS `bin` 目录加入当前 shell 的 `PATH`，再执行 Wails 打包：

```powershell
$env:PATH = "<project>/.aifd/tools/windows-amd64/nsis/3.10/bin;" + $env:PATH
wails3 package
```

默认工具版本由内置 manifest 提供：

```text
tools.protoc: 36.2
tools.protoc-gen-go: v1.36.12
tools.luban: v4.8.0
tools.redis-server: 8.6
tools.wails: v3.0.0-beta.34
tools.nsis: 3.10
```

如果 `aifd.lock` 中是 `1.x.x`、`2.x.x`、`any` 或 `*` 这类占位版本，安装时会使用默认
manifest 解析为精确版本；安装成功后写回 `aifd.lock`。`--dry-run` 只输出计划，不下载
安装包、不写文件。

`proto gen` 使用 `--go_out` 时会自动解析 `protoc-gen-go`，并把其 `bin` 目录加入本次
`protoc` 调用的 `PATH`。如果安装的 `protoc/include` 存在，也会自动追加为 proto import
path，用于 well-known proto。

## 更新边界

Tools 由 `aifd.lock` 锁定。Wails 3 升级必须同步 Go 依赖、CLI、前端 `@wailsio/runtime`、生成绑定和构建验收；不能只改工具版本。`tools install` 只写业务项目
本地 `.aifd/tools` 和 `aifd.lock`，不会把 exe 提交进仓库。命令应保持适合脚本调用；
除非命令名明确表示安装、生成或进程管理，否则应避免修改项目文件。

## 相关文档

- 命令和参数事实源：[CLI main.go](cmd/aifd/main.go)、[工具解析](internal/toolchain/toolchain.go)、[安装清单](internal/installer/manifest.go)。
- [项目首页](../README.zh-CN.md)
- [服务入口开发](../aifd-rules/skills/service-entrypoint-development/SKILL.md)
- [版本与更新策略](../docs/update-policy.md)
- [Schema 与 Proto 工作流](../aifd-rules/skills/schema-proto-workflow/SKILL.md)
- [配置工作流](../aifd-rules/skills/config-table-workflow/SKILL.md)
