# 业务项目接入

先根据产品需要选择框架能力。普通业务可以只用 RPC、RDB 或客户端，不要求启用账号中心、Runtime、
NATS、LLM 或自动证书。模块选择和源码入口见[文档导航](README.md)。

## 锁定版本与规则

在业务项目自己的 Go module 中锁定一个框架根版本，例如：

```sh
go get github.com/li9chuan/aifirstdev@v0.6.0
go install github.com/li9chuan/aifirstdev/aifd-tools/cmd/aifd@v0.6.0
```

原生 HTTPS/WSS 自 [v0.6.0](releases/v0.6.0.md) 提供。统一升级 `go.mod/go.sum`，并阅读对应版本的接入条件和能力边界；不要照搬仓库示例的本地 `replace`。
CLI 可按项目工具管理方式固定版本，`aifd update` 本身不检查或执行升级。

AI 开发先读同版本 `AGENTS.md` → `aifd-rules/AGENTS.md` → 所需 skill；业务项目自己的产品规则仍由业务拥有。
详细发布与独立消费约定见[更新策略](update-policy.md)。

## 组织业务与入口

业务 `proto/`、配置源和 `features/<name>/` 留在业务仓库；生成物放明确的 `generated/` 目录。
当前用户的数据块由 user module 拥有；跨用户关系、索引和任务由 service module 或业务模块拥有。
需要统一 User 驻留、保存接线或多模块恢复顺序时使用 [Runtime](../aifd-runtime/README.md)。

从 [Minigame](../aifd-examples/minigame/README.md)查同进程和 split 接线，从
[Desktool](../aifd-examples/desktool/README.md)查 Wails 壳和 Go binding；示例中的 demo 认证、内存状态与本地地址由业务替换。

宿主拥有启动和退出顺序：

1. 加载 typed 配置并校验，初始化本地日志，构造 DB、Bus 和业务依赖。
2. 恢复 service module，成功后注册路由、安装到 Bus，再启动 runner 和后台任务。
3. 显式启动公共 HTTP/WS 入口；业务恢复与传输均就绪后才对外报告可用。
4. 退出时先停止接入与后台生产者，完成 WS 关闭所需的 RPC 清理；关闭 RPC Bus 并等待 runner 排空，再关闭 handler 依赖的 Runtime、RDB 和客户端，最后关闭日志。

具体相互依赖由应用决定。`service.Service` 按传入顺序调用 hook，不自动检查调用阶段或按 `Depends` 排序，
也不自动回滚部分启动；入口需要处理每一步错误并关闭已创建资源。
RPC runner 的 context 用于处理消息，取消它不代替 Bus 关闭；具体约束见 [RPC 接线](../aifd-rpc/README.md)。

## 登录、请求与事件

采用框架账号中心时，流程是 `Verifier` 校验凭据 → Account 解析账号 → 业务 `TokenIssuer` 签名登录 payload →
Proxy 的业务 LoginHandler 验签并确定身份。账号中心不拥有业务 UID/角色映射。
已有认证体系可直接适配 LoginHandler 或 HTTP 握手认证，不必迁移到 Account。

业务 handler 默认使用 `func(*rpc.Session, *Req)`：本次结果用 `Response/Error` 回原连接，
独立用户事件用 `Push/PushTo`。Go 客户端 `New` 后先 `Start()` 再进行 await 请求；
认证成功后再启动有期限的业务心跳。连接失效后的重连、重新鉴权和事实重读由应用安排。
详见 [RPC](../aifd-rpc/README.md)、[Client](../aifd-client/README.md)和[多设备调用规则](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md)。

## 保存、配置和观测

- 普通业务通过 [RDB](../aifd-rdb/README.md) 的 `Save` 入队，由后台合并写入，通常不调用 `Flush`。
  停服时宿主先停止业务写入，再调用所创建 DB 的 `Close` 并处理错误，最后关闭共享 Redis client。
  RDB 与原生 Redis/Lua 的取舍见[业务持久化选型](../aifd-rules/skills/feature-development/references/rdb-selection.md)。
- [Config](../aifd-config/README.md) 的 `Reload` 还要检查 `result.Applied` 和 `Issues`；nil error 不一定代表已发布配置。
  已发布的 bundle 只读，宿主串行触发热更。
- [Observe](../aifd-observe/README.md) 的运行日志先 `logging.InitDefault`；Stat 需要配置 sink。
  observe-agent 只查询预注册文件和计数，不替代日志文件输出或业务真相。

## 公开 HTTPS/WSS

原生方案只改变入口装配和客户端 URL，现有业务 handler、PB 和权限规则继续复用。
已有 mux 交给 `httpserver.Start`，有 WebSocket 时提供对应关闭回调；多个 WS handler 由入口组合关闭。
Go 客户端使用 WSS 和 `RequireTLS: true`，浏览器使用正常 HTTPS/WSS 证书校验。

部署者提供域名、可达的公网 443 和版本目录外的私有持久缓存，先 staging 后 production。
同一个入口可以承载 WS、登录引导和下载；业务生成的公开 URL 也应使用 HTTPS。
首次证书未就绪时，公共 TLS 握手可能失败，不能依赖同一 HTTPS 地址读取 pending 状态。

配置与代码见[原生入口用法](../aifd-proxy/adapters/httpserver/README.md)，状态和存储归属见
[设计说明](native-https-design.md)。当前 CA 限流适配仍有明确缺口，不能将“自动续期已实现”理解为“所有官方限流要求已覆盖”。
