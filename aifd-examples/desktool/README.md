# desktool

## 定位

`desktool` 是一个 Wails 3 桌面软件框架示例，展示普通桌面应用如何组织登录、公开主界面和登录后工作台。

当前固定 `v3.0.0-beta.34`；仍为预发布版本。Go 依赖、CLI、`@wailsio/runtime` 和根目录 `aifd.lock` 必须保持一致。

它采用 React + Ant Design 编写前端界面。Ant Design MCP、CLI、LLMs.txt 和 X Skills 的定位是开发期 AI 辅助工具链，用来帮助 Codex 查询组件、示例、token、语义化 DOM 和迁移信息；这些工具不进入桌面软件运行时。

## 负责

- Wails 桌面壳入口、窗口参数、前端 assets、binding 注册和生命周期封装。
- `Bootstrap`、`Login`、`LoadWorkspace`、`CurrentSession`、`Logout` 五个桌面业务 API。
- `CheckUpdate`、`DownloadUpdate`、`InstallUpdate` 三个 Windows 整包更新 API。
- React + TypeScript + Ant Design 的应用壳、登录面板、公开主界面和示例工作台。
- 三个后端边界示例：
  - `cmd/account`：内部账号中心，校验 `demo / demo123`，维护账号身份和 provider 绑定模型。
  - `cmd/proxy`：客户端唯一后端入口，调用 account 和 business，并维护服务端 session。
  - `cmd/business`：业务服务，返回公开 bootstrap 数据和登录后 workspace 数据。

## 不负责

- 真实账号中心、权限系统、JWT、signed ticket 或远端认证。
- 运行时 AI Agent、A2UI command、`@ant-design/x-card` 或动态 UI 协议。
- 真实 WebSocket proxy、NATS RPC、rdb、robot、Luban 或配置表接入。
- 任何具体业务工具能力。
- 公共 `aifd-wails`、`aifd-desktop` 或 `aifd-ui` 抽象；等第二个桌面示例复用同类能力时再抽。
- macOS Sparkle、dmg/pkg 安装或 Linux 安装包更新。

## 使用方式

以下命令从本目录（`aifd-examples/desktool`）开始。安装锁定的前端依赖并构建：

```powershell
cd frontend
npm ci
npm run build
cd ..
```

`frontend/dist` 不提交到 Git；Go 入口通过 embed 引用它，必须先完成上述前端构建。
回到本目录后运行 Go 测试：

```powershell
go test -mod=readonly -count=1 -timeout=2m ./...
```

从仓库根目录按需安装 Wails 3 CLI，再把其目录加入当前 shell 的 PATH：

```powershell
go run ./aifd-tools/cmd/aifd tools install --tool wails
$env:PATH = "$PWD/.aifd/tools/windows-amd64/wails/v3.0.0-beta.34/bin;" + $env:PATH
cd aifd-examples/desktool
wails3 dev -config ./build/config.yml -port 5173
```

`wails3 build` 构建 Windows amd64 应用，`wails3 package` 构建 NSIS 安装包。
`Taskfile.yml` 统一调用 `build/build.ps1`，先校验锁定版本，再生成绑定、构建前端和 Go 程序。
单独重建 TypeScript 绑定可执行 `powershell -NoProfile -ExecutionPolicy Bypass -File ./build/build.ps1 -BindingsOnly`。
绑定由 `wails3 generate bindings -ts -i -names` 生成，不能手工编辑。

浏览器预览需在 `frontend` 目录显式运行 `npm run dev`（preview 模式）。
原生开发由 `wails3 dev` 启动桌面进程；`npm run dev:native` 只是它使用的前端服务，不会单独启动 Wails 窗口。
原生和生产模式只调用 Go 绑定，启动失败会显示错误，不自动切换到模拟数据。

Windows 发布包可使用 NSIS installer：

```powershell
wails3 package VERSION=0.1.2
```

NSIS 是 Windows 安装器，不是运行时热更新。`desktool` 的更新流程是：检查 manifest、下载新的 `.exe` 安装包、校验 sha256、启动安装器、让用户完成覆盖安装。

Wails 和 NSIS 需要预先准备；NSIS 可在 PATH 中提供，或通过 `AIFD_MAKENSIS` 指定。
也可在仓库根目录显式运行 `go run ./aifd-tools/cmd/aifd tools install --tool nsis`，按 [Tools 说明](../../aifd-tools/README.md)将安装目录加入 PATH。
打包命令不会自动安装系统工具，`VERSION` 同时写入程序版本、Windows 资源和安装器。
`build/bin`、`build/tmp`、临时 `.syso` 和 `build/windows/nsis/MicrosoftEdgeWebview2Setup.exe`
是本地构建输出。图标、manifest、安装脚本和生成的 TypeScript 绑定保留在版本控制中。
安装包构建成功不等于原生窗口、安装或更新流程已经通过运行验证。

示例 manifest：

```json
{
  "version": "0.1.2",
  "minVersion": "0.1.0",
  "force": false,
  "url": "https://example.com/desktool-0.1.2-setup.exe",
  "sha256": "...",
  "notes": ["修复登录流程", "优化工作台界面"]
}
```

生成安装包后可计算 sha256：

```powershell
Get-FileHash .\build\bin\desktool-amd64-installer.exe -Algorithm SHA256
```

把安装包和 manifest 放到任意静态 HTTP 服务后，客户端通过环境变量配置更新源：

```powershell
$env:DESKTOOL_VERSION="0.1.0"
$env:DESKTOOL_UPDATE_MANIFEST_URL="https://example.com/desktool/latest.json"
wails3 dev -config ./build/config.yml -port 5173
```

manifest 中的 `url` 必须是 HTTP(S) 绝对地址，并指向 Windows `.exe` 安装器；有新版本时 `sha256` 必须是 64 位十六进制字符串。`minVersion` 高于当前版本或 `force=true` 时，界面会显示强制更新。

可选启动三个示例后端：

```powershell
go run ./cmd/account
go run ./cmd/business
go run ./cmd/proxy
```

Wails 默认使用本进程 embedded 装配，所以上述三个命令用于展示服务边界，不是运行桌面示例的强依赖。

如果要让 Wails 只连接 proxy HTTP 入口，可使用 remote 模式：

```powershell
$env:DESKTOOL_MODE="remote"
$env:DESKTOOL_PROXY_URL="http://127.0.0.1:19082"
wails3 dev -config ./build/config.yml -port 5173
```

remote 模式下桌面端只访问 proxy。proxy 通过内部 HTTP client 调用 account 和 business，客户端不直接连接账号中心，也不保存账号中心 token。

## 登录边界

`desktool` 默认不使用 JWT 或 `aifd-auth` signed ticket。登录流程是：

```text
Wails binding
  -> proxy /api/login
  -> account /api/login
  -> proxy 内部 session
  -> business workspace
```

这里的 session 是 proxy 服务端状态，用来描述当前连接或 HTTP 会话是谁、能否读取工作台、退出时如何清理。React 前端只接收普通 `SessionState` 展示用户信息，不拿 account token。

`aifd-auth` 仍适合 `minigame` 这类需要 proxy 本地验签、减少账号中心热路径请求的游戏登录链路；它不是 `desktool` 的默认方案。

## 更新边界

`desktool` 可以跟随 Wails、React 或 Ant Design 升级，但功能必须围绕桌面框架形态保持清晰。
新增真实业务能力应放入业务项目，不应把本目录扩展成生产工具。

## 相关文档

- [示例入口](../README.md)
- [桌面应用开发](../../aifd-rules/skills/desktop-app-development/SKILL.md)
- [Ant Design UI 开发](../../aifd-rules/skills/ant-design-ui-development/SKILL.md)
