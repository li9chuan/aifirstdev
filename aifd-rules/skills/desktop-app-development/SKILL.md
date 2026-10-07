---
name: desktop-app-development
description: 开发 Wails、React、Ant Design 桌面应用或 desktool 示例时使用。适用于 Go binding、桌面应用壳、Go/JS 通信边界、登录流程、公开主界面、登录后工作台、整包更新、Wails CLI/NSIS 工具边界、account/proxy/business 服务边界、前端本地状态和桌面示例 README 更新。
---

# 桌面应用开发

## 核心定位

桌面应用运行时使用普通 Wails 3 + React + Ant Design 组件。Go binding 返回业务数据结构，前端用固定组件代码渲染；不要把运行时 UI 变成 A2UI command、XCard surface 或在线 Agent 动态生成界面，除非用户明确要求做“运行时 AI 助手 UI”专项示例。

Wails 3 当前锁定 `v3.0.0-beta.28`。Go module、CLI、前端 `@wailsio/runtime`、`aifd.lock` 与默认安装清单必须同步；升级需重新生成绑定并验收原生窗口和打包。

## 默认分层

- `cmd/account` 只负责账号、密码、provider、token 或 ticket；它不拥有桌面窗口，也不依赖 proxy。
- `cmd/proxy` 只负责连接、认证边界、session、未登录公开请求和转发；它不拥有账号中心实现。
- `cmd/business` 只负责业务数据、公开 bootstrap 数据和登录后 workspace 数据；它不返回 UI command。
- Wails `DesktopApp` binding 是本地门面，负责调用 account/proxy/business 并返回 `BootstrapData`、`LoginResult`、`SessionState`、`WorkspaceData` 等普通结构。
- 前端只写 React 组件和本地状态，不直接拼后端 token 规则，不绕过 binding 调内部服务。

## Wails 壳边界

- 桌面壳只拥有 `application.New`、窗口配置、前端 assets、`application.Service` 注册和生命周期；使用 Wails 3 的应用/窗口事件及 shutdown callback。
- `main.go` 应尽量只做装配：创建业务 binding，调用内部 `shell.Run` 或等价封装；不要在入口里堆 account/proxy/business 逻辑。
- 首个桌面示例先在业务示例内部封装壳能力；只有第二个桌面示例稳定复用后，才考虑提升为 `aifd-desktop/adapters/wails`。
- `shell` 不依赖 account、proxy、business、updater 或具体 UI 数据结构；业务 binding 自己组合这些服务。

## Go 与 JS 边界

- WS 连接、PB/packet 编解码、重连、登录态、缓存、数据分析、长期任务和本地文件访问默认放 Go 侧封装。
- React 只负责 Ant Design 组件、表单输入、按钮、表格、图表、轻量交互态和展示态。
- 前端通过 Wails binding 调 Go 方法，通过 Wails event 接收 Go 推送的 view model；不要让前端直接维护 proxy WebSocket、PB、inbox 或后端协议细节。
- 只有显式 Vite preview 模式可使用 fallback backend；原生和生产模式调用 Wails 3 生成的 TypeScript 绑定，绑定失败必须显示错误，不能静默改用模拟数据。用 `wails3 generate bindings -ts -i -names` 重建绑定，禁止手改生成物。
- 验证 UI 时要区分“Web 预览可跑”和“Wails 原生窗口可跑”；前者不代表 Go binding、WebView2、安装包或更新流程已验证。

## 更新策略

- 本示例保留既有整包客户端更新方案；迁移 Wails 版本不同时更换更新协议，不做运行时替换 Go 代码。
- 首版推荐流程：检查 manifest -> 下载 installer -> sha256 校验 -> 启动安装器 -> 用户完成覆盖安装或重启。
- Windows 使用 NSIS installer；macOS 后续再考虑 dmg/pkg 或 Sparkle；Linux 后续再考虑 AppImage、deb、rpm 或 tar.gz。
- `updater` 不依赖登录态，不理解业务服务，不访问 account/proxy/business；它只处理版本、下载、校验和安装启动。
- 前端只展示更新状态和触发 binding；不要在 JS 中直接下载安装包、校验 hash 或启动安装器。

## 实现规则

- 启动后先进入桌面壳界面，再显示公开主界面和可选登录面板；不要把登录页做成唯一首屏。
- 未登录可读取公开基础数据；登录后再加载 workspace 数据。
- demo 登录可以使用内存账号和 session，但 README 必须说明这不是生产认证。
- 保持运行时接口窄：`Bootstrap`、`Login`、`LoadWorkspace`、`CurrentSession`、`Logout` 足够覆盖最小桌面示例时，不新增泛化 action 总线。
- 更新入口保持窄：`CheckUpdate`、`DownloadUpdate`、`InstallUpdate` 足够覆盖整包更新时，不新增泛化 shell action 总线。
- 桌面示例不承载真实业务工具能力；真实业务应放进业务项目或独立示例。

## 前端规则

- 使用 `ant-design-ui-development` skill 查询 Ant Design 组件、demo、token、semantic DOM 和升级信息。
- 运行时依赖普通 `antd`，不默认引入 `@ant-design/x-card`。
- 组件布局优先用应用壳、侧边导航、顶部状态、内容区、登录面板和工作台表格；不要做营销 landing page。
- 前端类型要与 Go binding JSON 字段保持一致；字段改名时同步更新 TypeScript 类型、Go 测试和 README。

## 验证

- Go 侧运行 `go test ./...`。
- 前端依赖可用时运行 `npm run build`。
- 搜索确认运行时代码不包含 `A2UI`、`XCard`、`@ant-design/x-card`、`UIResult.Commands`。
- Wails CLI 通过 `aifd tools install --tool wails` 按需安装，不进入默认工具集合；优先使用项目本地 `.aifd/tools/.../wails/.../bin/wails3`。
- 原生开发使用 `wails3 dev -config ./build/config.yml -port 5173`；Windows 发布包使用 `wails3 package`，任务定义和生成器必须与锁定版本一致。
- NSIS 属于显式安装的 Windows 打包工具，不自动安装系统级组件；无法构建安装包时说明缺失原因。
