---
name: ant-design-ui-development
description: 使用 Ant Design React 组件开发前端 UI 时使用。适用于查询组件 API、demo、design token、semantic DOM、changelog、doctor/lint/migrate，配置 Ant Design MCP、CLI、LLMs.txt，或判断 X Skills 与运行时 UI 的边界。
---

# Ant Design UI 开发

## 工具优先级

1. MCP 优先：AI IDE 已接入 MCP 时，使用 `antd mcp` 暴露的组件知识查询组件、demo、token、semantic DOM 和 changelog。
2. CLI 次选：没有 MCP 或需要脚本化诊断时，用 `@ant-design/cli` 的 JSON 输出。
3. LLMs.txt 兜底：MCP/CLI 不可用、离线环境或只需要静态上下文时，读取官方 LLMs.txt。
4. X Skills 作为开发期方法论参考；不要把 X Skills 或 A2UI 理解为业务应用运行时依赖。

## 常用 CLI 查询

在前端项目内使用这些命令，输出优先选择 JSON，便于 AI 解析：

```powershell
antd info Button --format json --lang zh
antd demo Form basic --format json --lang zh
antd token --format json --lang zh
antd semantic Table --format json --lang zh
antd changelog --format json --lang zh
antd doctor --format json --lang zh
antd lint ./src --format json --lang zh
```

如果本地未安装 CLI，可先用：

```powershell
npm install -g @ant-design/cli
```

是否全局安装由开发环境决定；仓库运行时不要依赖全局 CLI。

## 编码规则

- 生成普通 React + TypeScript + `antd` 组件代码；不要默认生成 A2UI command 或 `@ant-design/x-card` 运行时渲染。
- 查询组件后只使用需要的 API，不把 demo 大段照搬进业务代码。
- 对表单、表格、列表、布局、统计卡片和状态提示，优先使用 Ant Design 已有组件。
- 主题 token、语义化 DOM、组件变更和迁移信息以官方 MCP/CLI/LLMs 结果为准。
- 组件事件回调应调用业务层清晰动作，例如 `Login`、`Logout`、`LoadWorkspace`，不要创建无边界的 action 总线。
- 在 Wails 桌面应用中，Ant Design 只负责界面表达；WS、PB、缓存、数据分析、更新下载和安装启动都应通过 Go binding 承载。
- 需要支持浏览器预览时，可以写 fallback backend，但它只能模拟 Go binding 返回的普通数据结构，不能成为真实运行时协议实现。

## 边界判断

- 如果需求是“让 Codex 更好地开发 Ant Design UI”，使用本 skill 和官方 MCP/CLI/LLMs.txt。
- 如果需求是“应用运行时内置 AI 助手，并动态返回 UI 描述”，另开运行时 AI UI 方案，不要混入普通 desktool。
- 如果需求只是普通桌面软件或后台管理界面，保持运行时为固定 React 组件和普通业务数据。
- 如果需求涉及 Wails 壳、Go binding、更新流程、NSIS 或桌面后端通信，叠加 `desktop-app-development`；不要在本 skill 中定义桌面运行时协议。
