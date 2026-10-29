# aifd-examples

## 定位

`aifd-examples` 保存不同业务形态的示例，用于展示框架模块如何组合到真实应用结构中。

## 负责

- 完整游戏后端框架展示项目。
- 桌面工具示例项目。
- 框架 API 的参考用法。
- 展示本地 adapter 和工作流边界的测试。
- 展示 user runtime 和 service lifecycle 可以用同进程或拆分进程两种部署形态组合。

## 示例

- `minigame`：完整游戏后端框架示例，使用 game/world 业务服务名展示账号、proxy、
  玩家根文档、service module、配置、observe、server/client/robot 入口、all-in-one 和 split 部署。
- `desktool`：Wails 桌面工具示例，展示桌面应用壳、Go binding、前端状态流和登录前后状态。
- [native-https](native-https/README.md)：根模块内的最小传输示例，展示原生 HTTPS/WSS、免费证书自动管理及退出清理；不新增独立 Go module。

## 不负责

- 生产 feature 代码或业务专属玩法系统。
- 真实业务 `.proto` 文件、Luban 表、生成的 pb 或生成的 cfgtable 代码。
- 应由对应 `aifd-*` 模块拥有的共享框架抽象。

## 业务项目如何使用

业务项目可以把示例作为模块接线起点，但真实 feature 代码、schema 和配置应放入自己的
仓库或服务目录。

## 更新边界

示例应围绕各自目标保持清晰：`minigame` 覆盖完整游戏后端框架链路但不扩展成完整玩法内容，
`desktool` 聚焦桌面工具形态。公共框架 API 变化时示例可以跟随调整，但不承诺生产行为兼容。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Minigame 示例](minigame/README.md)
- [Desktool 示例](desktool/README.md)
