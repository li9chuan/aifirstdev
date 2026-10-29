# 文档与代码导航

先确认正在阅读的源码版本。模块 README 描述该快照的用法；[发布说明](releases/v0.6.0.md)记录已发布版本，
[未发布变更](releases/unreleased.md)不代表旧版依赖已有能力。业务项目优先读取所锁定版本的文档和规则。

## 按任务找入口

先读“用法与设计”，需要修改时再沿源码入口和对应 skill 深入，不需要预先读完整仓库。

| 任务 | 用法与设计 | 源码入口 |
|---|---|---|
| 新业务项目、选择同进程或拆分服务 | [业务接入](application-integration.md)、[示例](../aifd-examples/README.md) | [minigame 装配](../aifd-examples/minigame/internal/app/allinone.go) |
| 判断职责归属、最小公共契约 | [模块边界](module-boundary.md)、[Contracts](../aifd-contracts/README.md) | [模块描述](../aifd-contracts/module.go) |
| 账号登录、Redis 身份映射 | [Account](../aifd-account/README.md) | [登录服务](../aifd-account/service.go)、[Redis store](../aifd-account/redis_store.go) |
| payload 签名、本地验签与换 key | [Auth](../aifd-auth/README.md) | [token](../aifd-auth/token/token.go) |
| Go 客户端、await、push、心跳 | [Client](../aifd-client/README.md) | [调用门面](../aifd-client/client.go)、[WS transport](../aifd-client/adapters/proxyws/websocket_transport.go) |
| 浏览器客户端与 PB packet | [Web SDK](../aifd-proxy/web/README.md)、[Proxy 协议](../aifd-proxy/README.md) | [Web client](../aifd-proxy/web/src/client.ts)、[codec](../aifd-proxy/codec/packet.go) |
| 网关登录、租约、多设备投递 | [Proxy](../aifd-proxy/README.md)、[调用与多设备规则](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md) | [WS server](../aifd-proxy/adapters/websocket/server.go)、[租约](../aifd-proxy/runtime/client_sessions.go) |
| 原生 HTTPS/WSS、证书申请和续期 | [接入与配置](../aifd-proxy/adapters/httpserver/README.md)、[设计与边界](native-https-design.md)、[可运行示例](../aifd-examples/native-https/README.md) | [listener](../aifd-proxy/adapters/httpserver/server.go)、[ACME](../aifd-proxy/adapters/httpserver/acme.go)、[原子资产](../aifd-proxy/adapters/httpserver/assets.go) |
| RPC 注册、原连接回包、NATS | [RPC](../aifd-rpc/README.md)、[Proxy-Broker](../aifd-proxy/README.md#proxy-broker-拓扑) | [注册门面](../aifd-rpc/registry.go)、[Session](../aifd-rpc/session.go)、[NATS](../aifd-rpc/adapters/nats/bus.go) |
| User 数据块、Service 生命周期、关闭顺序 | [Runtime](../aifd-runtime/README.md)、[组织原则](scope-runtime.md) | [UserRuntime](../aifd-runtime/user/service.go)、[Service](../aifd-runtime/service/service.go)、[入口 helper](../aifd-runtime/entrypoint/runtime.go) |
| protobuf 保存、Redis/Lua 选型、TTL、Flush、失败恢复 | [持久化选型](../aifd-rules/skills/feature-development/references/rdb-selection.md)、[RDB](../aifd-rdb/README.md) | [DB](../aifd-rdb/db.go)、[Saver](../aifd-rdb/saver.go)、[journal](../aifd-rdb/journal.go) |
| typed 配置、校验与热更 | [Config](../aifd-config/README.md)、[Viper](../aifd-config/adapters/viper/README.md) | [Manager](../aifd-config/runtime/manager.go)、[Viper loader](../aifd-config/adapters/viper/loader.go) |
| proto 兼容、生成与路由绑定 | [Schema](../aifd-schema/README.md)、[生成命令](../aifd-tools/README.md) | [协议源目录](../aifd-schema/proto)、[兼容性检查](../aifd-schema/runtime/validator.go)、[绑定](../aifd-schema/runtime/binder.go) |
| 本地日志、Stat、observe-agent | [Observe](../aifd-observe/README.md) | [日志初始化](../aifd-observe/logging/config.go)、[agent](../aifd-observe/agent/agent.go)、[admin RPC](../aifd-observe/adapters/agentrpc/rpc.go) |
| LLM Complete/Stream 与 provider 适配 | [LLM](../aifd-llm/README.md) | [契约](../aifd-llm/client.go)、[adapter](../aifd-llm/adapters) |
| 测试机器人、行为树、report | [Robot](../aifd-robot/README.md) | [RunGroup](../aifd-robot/runtime/rungroup.go)、[report](../aifd-robot/contracts/report.go) |
| 可选邮件 owner、领取与迁移 | [Mail](../aifd-plugins/mail/README.md)、[接入规则](../aifd-rules/skills/runtime-module-development/references/mail-owner.md) | [service](../aifd-plugins/mail/service/module.go)、[user](../aifd-plugins/mail/user/module.go)、[mailbox](../aifd-plugins/mail/mailbox) |
| Wails 桌面、登录与整包更新 | [Desktool](../aifd-examples/desktool/README.md) | [Go binding](../aifd-examples/desktool/internal/desktop/app.go)、[壳](../aifd-examples/desktool/internal/shell/shell.go)、[updater](../aifd-examples/desktool/internal/updater/updater.go) |
| CLI、工具安装与框架升级 | [Tools](../aifd-tools/README.md)、[更新策略](update-policy.md) | [CLI](../aifd-tools/cmd/aifd/main.go)、[默认安装清单](../aifd-tools/internal/installer/manifest.go) |

开发和审查从 [AGENTS](../AGENTS.md)进入；[skills 索引](../aifd-rules/skills/README.md)选择工作流，
[评审清单](../aifd-rules/checklists/review.md)按范围使用。字段和签名查对应源码，不维护第二份完整 API 清单。

## 找验证证据

| 要验证什么 | 入口与覆盖范围 |
|---|---|
| 根模块、两个独立示例、前端 | [首页构建说明](../README.zh-CN.md#构建与验证)、[verify.ps1](../scripts/verify.ps1)；根目录 `go test ./...` 不包含两个嵌套 Go module |
| 外部 NATS 连接与 split 示例 | 外部连接用例设置 `AIFD_NATS_URL`，参考 [Minigame](../aifd-examples/minigame/README.md)；自带 broker fixture 的用例按各自 `_test.go` 运行，跳过不是通过 |
| 邮件真实 Redis 恢复 | [Mail 验证](../aifd-plugins/mail/README.md#验证)；可用 `AIFD_MAIL_REDIS_REQUIRED=1` 要求依赖缺失即失败 |
| ACME 真实签发、ARI、自动续期 | [HTTPS 验证](../aifd-proxy/adapters/httpserver/README.md#验证)；需要 `integration` tag、Pebble 和相应环境变量 |
| 桌面窗口、安装器与更新 | [Desktool](../aifd-examples/desktool/README.md)；前端构建或 Go 测试不代替原生运行验收 |

测试名称和断言以拥有行为的包内 `_test.go` 为准。文档中的历史通过记录只覆盖当时列出的环境；
不把本地 CA、交叉编译或跳过的集成用例写成公网部署和生产运行证明。

## 文档分工

- 模块 README：能力、最小接入、生命周期、失败语义和限制。
- `docs/`：跨模块接入与设计决策；`docs/releases/`：版本变化、迁移及未发布状态。
- skill/reference：AI 修改代码时应遵守的约束，只链接所需用法和设计，不复制 API 手册。
- 示例：可运行接线；业务项目保留自己的协议、配置、权限和发布流程。

行为变化时同步拥有模块的说明和本导航。历史发布说明保留原版本事实，不随当前实现重写。
