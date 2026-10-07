# aiFirstDev

[English](./README.md) | 简体中文

**v0.1.0 是首个开发快照，API 仍可能发生不兼容变更。**
正式仓库为 [li9chuan/aifirstdev](https://github.com/li9chuan/aifirstdev)。
桌面示例当前面向 Windows amd64，使用 Wails 3 beta.28。

aiFirstDev 是面向 AI 优先开发的轻量后端框架，多 Go module 组织在同一个
workspace 中。当前 runtime 先覆盖游戏后端，但框架组织语言使用 service/root
runtime，业务应用按领域命名自己的后端服务，便于扩展到 Wails 工具、桌面应用和其他业务后端。

第一阶段拆分的目标是让基础设施模块保持独立，同时业务项目仍然按正常方式编写
feature 代码：

- [aifd-contracts](aifd-contracts/README.md)：最小公共契约。
- [aifd-auth](aifd-auth/README.md)：账号中心和 proxy 共享的签名 ticket。
- [aifd-account](aifd-account/README.md)：登录身份、全局 account_id、provider 绑定和登录 payload/token 签发；业务 uid、owner、realm 映射由业务项目维护。
- [aifd-client](aifd-client/README.md)：Go 客户端连接、登录、消息路由和请求。
- [aifd-config](aifd-config/README.md)：typed 配置 bundle 生命周期、校验、manifest diff 和 Luban bundle builder 适配。
- [aifd-llm](aifd-llm/README.md)：provider-neutral 的 LLM 请求、响应、stream、错误和 adapter 契约。
- [aifd-observe](aifd-observe/README.md)：可观测事件、异步 sink 和本地统计适配。
- [aifd-proxy](aifd-proxy/README.md)：客户端网关契约、session、投递和适配。
- [aifd-robot](aifd-robot/README.md)：集成测试机器人、运行组、行为和统计。
- [aifd-rpc](aifd-rpc/README.md)：RPC 契约、路由、分发器和适配。
- [aifd-rdb](aifd-rdb/README.md)：持久化 saver 原语和存储适配。
- [aifd-runtime](aifd-runtime/README.md)：service/root runtime、root store、module lifecycle 和配置/lifecycle 辅助。
- [aifd-schema](aifd-schema/README.md)：proto/message 描述、兼容性检查和 RPC 绑定。
- [aifd-tools](aifd-tools/README.md)：CLI、工具安装和预留的更新命令。
- [aifd-rules](aifd-rules/README.md)：AI 规则、skills、模板和 checklist。
- [aifd-plugins/mail](aifd-plugins/mail/README.md)：可选邮件存储、邮箱访问和 runtime 模块，由应用入口显式组合。
- [aifd-examples](aifd-examples/README.md)：业务形态示例；`minigame` 展示完整游戏后端框架链路，`desktool` 展示桌面工具。

依赖方向必须保持单向：

```text
aifd-contracts        aifd-llm
  ^                     ^
  +-- aifd-auth         |
  +-- aifd-account      |
  +-- aifd-config       |
  +-- aifd-observe      |
  +-- aifd-proxy        |
  +-- aifd-robot        |
  +-- aifd-rpc          |
  +-- aifd-rdb          |
  +-- aifd-schema       |
  ^                     |
aifd-runtime           |
  ^                     |
业务项目 ---------------+
```

`aifd-config`、`aifd-llm`、`aifd-observe`、`aifd-proxy`、`aifd-robot`、
`aifd-rpc`、`aifd-rdb` 和 `aifd-schema` 不理解 player、world、feature、DBPlayer、
具体 `.proto` 文件、真实 Luban 表或业务 prompt 策略。`aifd-runtime` 负责提供
`service.Service` 和 `root.Runtime`；游戏、玩家、世界等词汇只属于业务示例，不是框架公共 API。

当前仍处于新框架设计阶段。框架 API 优先破坏式收敛到最小业务门面，不为旧实验接口保留
兼容 wrapper；复杂度应沉到框架层，业务项目只接触必要的业务参数和领域对象。

模块分类和未来 `aifd-core` 准入规则见 [docs/module-boundary.md](docs/module-boundary.md)。
Scope runtime 的组织原则见 [docs/scope-runtime.md](docs/scope-runtime.md)。

## 文档导航

- AI 开发与审查：从[Agent 规则](AGENTS.md)进入；[rules 使用入口](aifd-rules/README.md)说明资料分工，[skills 索引](aifd-rules/skills/README.md)按任务选择流程。
- 代码审查：[项目与 Feature 代码评审](aifd-rules/skills/runtime-feature-review/SKILL.md)按范围引用已有[评审清单](aifd-rules/checklists/review.md)。
- 设计依据：[模块边界](docs/module-boundary.md)、[runtime 组织原则](docs/scope-runtime.md)、[更新策略](docs/update-policy.md)。上方模块链接进入各自用法和相关工作流，接线参考[示例入口](aifd-examples/README.md)。

## 快速开始

AI 优先指开发工作流：通过仓库规则、skills、模板和可复现检查，帮助 AI 在模块边界内开发。
应用运行时不要求接入 LLM；`aifd-llm` 是可选模块。

安装 Go 1.26 或更新版本和 Git，启动内存存储后端示例：

```powershell
git clone https://github.com/li9chuan/aifirstdev.git
cd aifirstdev/aifd-examples/minigame
go run ./cmd/minigame
```

另开终端，进入同一个 `aifd-examples/minigame` 目录：

```powershell
go run ./cmd/client -uid 1001 -counter-delta 1
```

在刚启动的服务上，客户端会输出 `auth ret=0` 和计数器 `value=1`。
默认使用本地 RPC 和内存存储，无需启动 Redis 或 NATS。按 Ctrl+C 停止服务。
分离服务模式见 [minigame](aifd-examples/minigame/README.md)，桌面示例见
[desktool](aifd-examples/desktool/README.md)。桌面构建还需要 Node.js/npm 和固定版本的
Wails CLI；Windows 安装包构建需要 NSIS。

## 在其他 Go 项目中使用

在本仓库以外已经初始化的 Go module 中，按需引入模块：

```powershell
go get github.com/li9chuan/aifirstdev/aifd-runtime@v0.1.0
go get github.com/li9chuan/aifirstdev/aifd-client@v0.1.0
go install github.com/li9chuan/aifirstdev/aifd-tools/cmd/aifd@v0.1.0
```

仓库根目录没有 Go module。17 个模块保留独立路径，统一位于
`github.com/li9chuan/aifirstdev/` 下；本地开发通过 `go.work` 组合。
子模块标签带目录前缀，例如 `aifd-runtime/v0.1.0`、`aifd-plugins/mail/v0.1.0`；
根标签 `v0.1.0` 标识整仓源码快照。本次首发不承诺 API 稳定，也不建立并行维护分支。
详见[版本与更新策略](docs/update-policy.md)。

`aifd update` 目前是预留占位入口，只打印提示，不检查、下载或执行更新。
第三方工具版本继续由 `aifd.lock` 固定。

## 构建与验证

需要 Go 1.26 或更新版本、Node.js 22.12 或更新版本及 npm，以及 PowerShell 5.1 或 7。
将 `go`、`node`、`npm` 加入 PATH，在仓库根目录执行：

```powershell
.\scripts\verify.ps1
```

如果 Windows PowerShell 5.1 阻止本地脚本执行，可使用
`powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1`，仅对本次进程生效。

脚本使用 `npm ci` 安装锁定的前端依赖，测试并构建 desktool 前端和 proxy Web 客户端，
再遍历 `go.work` 中的模块，分别执行 `go test -mod=readonly -count=1 -timeout=2m ./...`。
根目录直接执行 `go test ./...` 不会遍历这个多模块 workspace。首次执行需要联网下载依赖。

desktool 的 Go 入口 embed `frontend/dist`，因此必须先构建前端，再运行该模块的 Go 测试。
前端构建产物和安装器下载文件只在本地生成，不纳入版本控制；protobuf 源码、生成绑定和依赖锁文件保留在仓库中。

未设置 `AIFD_NATS_URL` 时，两个外部 NATS 集成测试会跳过。设置为可连接的测试 NATS 服务地址后才会运行，
验证脚本不会启动外部服务。原生桌面运行和 Windows 安装包构建是独立检查，见 [desktool](aifd-examples/desktool/README.md)。

## 许可证

Copyright 2026 aiFirstDev contributors。项目使用 [Apache-2.0](LICENSE) 许可证。
第三方组件保留各自许可证；桌面打包素材和模板的归属见
[第三方声明](aifd-examples/desktool/THIRD_PARTY_NOTICES)。
