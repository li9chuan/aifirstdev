# aifd-proxy

## 定位

`aifd-proxy` 定义客户端网关边界：认证连接、session 路由、消息投递，以及进入或
离开后端的流量 adapter 契约。

## 负责

- Connection 和 session 契约。
- Gateway 投递和路由原语。
- 可用的 Local、WebSocket 和 NATS 下行 adapter；`adapters/tcp` 当前仅为包占位，没有 TCP listener 或分帧实现。
- WebSocket binary frame 接入、登录状态、重复登录策略和断线清理。
- 客户端 wire protocol 对 `aifd-schema` 中通用 PB 帧头语义的使用。
- 可选 proxy-broker 入口能力：`adapters/nats/broker` 只封装 embedded NATS server
  生命周期，供 proxy 进程显式组合。
- 可选[原生 HTTP/HTTPS/WSS 入口](adapters/httpserver/README.md)：复用现有 handler，支持手动证书与 ACME 自动申请、持久化、续期及显式关闭。

## 不负责

- Player 状态、World 状态、DBPlayer 或玩法决策。
- 超出 gateway/session 身份之外的 feature 授权规则。
- 业务消息 schema 或持久化。
- 业务登录 PB 的解析、签名 token 校验和登录成功/失败 PB 内容。
- game、world 或其他业务服的 broker 生命周期；业务服应作为 NATS client 接入。

## 业务项目如何使用

业务项目使用此模块承载或适配客户端连接，然后把框架中立的 envelope 转发给 RPC 或
runtime 服务。玩法代码应放在目标服务之后，不放在 proxy 中。

前端与 proxy 的 PB 帧头定义在 `aifd-schema/proto/aifd/proxy/proxy.proto`。
协议事实源是 [proxy.proto](../aifd-schema/proto/aifd/proxy/proxy.proto)：`len` 是 body 长度，`inbox` 是 RPC 回调编号，
`topic` 是路由主题，`pbType` 是 body 的 protobuf 结构名。

客户端上行 `MsgHead.topic` 必须使用 `Mc` 开头的业务协议名，例如 `MceAuth`、
`MceInventoryInfo`。上行 topic 只能包含 ASCII 大小写字母，不能包含 `.`
、`_`、`-`、数字、空格或其他特殊字符。`C.*` / `S.*` 不是客户端协议命名；
后端内部如果使用 `S.<uid>` 这类 subject 前缀，只属于 RPC/NATS 投递层，不进入
客户端 packet。

服务端下行 `MsgHead.topic` 由后端生成，不强制 `Ms` 前缀。业务可以使用
`MseAuth` 这类协议名，也可以返回 `RPCError`、`StringValue` 或其他框架错误包 /
await 回包 topic。`pbType` 仍然只表达 body 的 protobuf 结构名，不承担路由语义。

首版 packet codec 使用 `protobuf(MsgHead) + protobuf(body)`。`topic` 和 `pbType`
保持结构化分离；当 `topic` 为空且 `pbType` 非空时，框架会把 `pbType` 作为默认
`topic`，但不会解析或生成 `123.PBType` 这类混合字符串。

上行消息经 codec 转为 `rpc.Message` 后进入 `aifd-rpc`：`Message.Name` 是 topic，
`Header["pbType"]` 是 protobuf 结构名，`Header["s"]` 是前端 await inbox。下行消息
按同样字段重新编码为 `MsgHead` 后写回客户端连接。

`Session.Error(code, message)` 的失败回包保留 `RPCError` topic 和请求 inbox。
WS codec 以 RPC Header 中的 `Status` / `errorMessage` 为真相，将它们编码为
`aifd.proxy.RPCError { code, message }` body，并设置 `pbType=RPCError`；解码时还原
这两个 Header。空错误码按 `INTERNAL` 处理；损坏的非空错误 body 按协议
解码失败处理。`MsgHead` 四个字段和帧格式不变，不传输其他 RPC Header。

Go 和 [Web 客户端](web/README.md) 会把匹配 inbox 的 `RPCError` 识别为失败；
收到旧服务端的空错误包时，返回 `INTERNAL / rpc request failed`。保留原错误码和
消息需要服务端和客户端都使用修复后的实现；仅升级服务端不能修复旧 Go 客户端
的错误识别。普通成功响应允许空 protobuf body，不以 body 是否为空判断成败。

WebSocket adapter 使用 `github.com/coder/websocket`，只接受 binary frame。支持登录包
`LoginHandler` 和 upgrade 前的 `HandshakeHandler` 两种认证入口，至少配置一种。使用登录包时，
业务解析登录 PB、验签并返回 `Identity` 和可选回包。认证成功后才将连接注册进 `Gateway`，并订阅 UID 下行。

未登录缺省只接受登录 topic；显式配置 `PublicTopics` 和 `PublicHandler` 可开放指定匿名请求。
该分支直接返回当前连接的回包，不建立身份、UID 订阅或业务连接租约。PublicHandler 只用于未登录分支，
登录后的普通请求仍走网关路由。只配置 PublicHandler 不能替代上述认证入口。

缺省带有登录后本地 `McePing` / `MsePong`，空的 PingTopic/PongTopic 会补默认值。
本地 Ping 不进入业务 RPC，也不续期 UserRuntime 连接租约；续租使用不同的业务心跳 topic，
或启用 Runtime 的 `WithDefaultHeartbeat()` 处理 `MceSessionHeartbeat`。

推荐业务使用 typed login 简写入口：

```go
websocket.NewServer(websocket.Config{
    Gateway:      gateway,
    Delivery:     delivery,
    LoginHandler: proxy.NewLoginHandler(authproxy.Authenticate(bus, tokenVerifier)),
})
```

框架会从请求 PB 类型名推导登录 topic，例如 `MceAuth`，并要求它满足客户端上行
`Mc*` 规则；从响应 PB 类型名推导回包 topic，例如 `MseAuth`，但不强制 `Ms*`。
登录失败默认发送失败回包后关闭连接；只有需要同连接重试登录时才使用高级
`NewTypedLoginHandler` 和 `KeepOpenOnFailure`。

`MceAuth` / `MseAuth` 这类真实登录协议属于业务项目自己的 proto。`aifd-proxy`
只提供 `LoginHandler` 入口和 packet 转发能力，不把业务登录 PB 放进框架层。账号中心
签发 token，proxy 本地验签，不应在每次登录热路径查询账号中心。

重复登录策略通过配置选择：

- `DuplicateKickOld`：踢旧保新，适合默认游戏客户端。
- `DuplicateAllow`：允许同 UID 多连接。
- `DuplicateRejectNew`：保留旧连接，拒绝新连接。

需要更细的规则时实现 `DuplicateHandler`。配置 `Bus` 时，框架默认通过 Open/Close RPC
维护业务 owner 的连接租约；高级适配可显式替换 `SessionLifecycle` 的 OnConnect/OnDisconnect。

### 握手、租约与多设备

`Config.HandshakeHandler` 可在 upgrade 前鉴权 HTTP 凭据。没有适用凭据返回
`Handled=false`，进入未登录处理分支，要完成登录包认证仍需配置 LoginHandler；已提供凭据认证失败返回 error，不能降级。业务返回
Identity 和可选 InitialMessage，消息内容属于业务。两种认证共用重复登录策略、UID
订阅、连接 Open、读循环和幂等清理。订阅、Open 或就绪消息失败会回滚连接。

配置 `Bus` 后，Server 可默认创建 Delivery、Gateway、RPCUpstream 和标准生命周期桥。
显式分组接线使用 `NewRPCUpstream(bus, delivery)`。有客户端 inbox 的请求通过
Bus.Request 取得结果，再送回原连接；没有 inbox 使用 Tell。成功或错误都不改投
同 UID 的其他连接。传输 Status（包括 NATS 无响应服务）统一编码为 RPCError。

UID 事件 `Push/PushTo` 的 inbox 为 0，向所有持有该 UID 连接的 Proxy 发布。
NATS Downlink 使用普通 PubSub；local Downlink 直接订阅 local Bus 的 UID 事件。
第一条连接订阅，最后一条关闭后退订，成员变更与订阅寿命共同协调。

`ClientSessionRef` 由 Proxy 启动实例 ID 和连接 ID 组成，只通过可信内部 metadata
携带，不进入客户端 MsgHead。Open 有最长 15 秒的期限，Close/过期引用保留至少
相同长度的退休窗口，拒绝队列中迟到的 Open。续期只能作用于既有有效引用。
业务服重启后重新鉴权连接；驻留 User 或 UID 数据库记录不能推断在线状态。
租约循环显式 Run，回调在锁外执行；回调错误会记录，不会停止其他连接到期清理。

业务选择请求响应或事件，不手工拼 inbox/header。规则和多设备验收见
[用户调用与多设备投递](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md)。

### Proxy-Broker 拓扑

生产需要省掉独立 broker 部署环节时，可以让 proxy 进程同时作为 NATS broker 节点：

- proxy 入口显式启动 `adapters/nats/broker`，并通过原生 NATS config 文件配置
  `listen`、`cluster.listen`、`routes`、broker 端 auth/TLS/JetStream 等生产参数。
- proxy 自己的 RPC bus 使用 `nats.go` `InProcessServer`，避免 `proxy -> 本机 NATS TCP`
  这一跳。
- game/world/其他业务服只配置 NATS client servers，连接 proxy-broker 集群，不启动
  embedded broker，也不拥有 broker shutdown 顺序。client 端凭据和 TLS 文件由 RPC
  adapter 的 NATS client 配置表达，不读取 nats-server 配置文件。

这个能力不通过 import 副作用启动；只有应用入口显式 wiring 后才生效。

### 原生 HTTPS/WSS

已有 `websocket.Server` 仍是 HTTP handler。宿主将同一 mux 交给 `httpserver.Start`，
并传入 WS 关闭回调；不在 LoginHandler、消息编解码或业务 feature 内管理证书。
详细接入见[原生入口](adapters/httpserver/README.md)，职责、持久化和未完成项见[设计说明](../docs/native-https-design.md)。

## 更新边界

Proxy 升级应保持 session 和 delivery 契约。adapter 变更不能要求业务 feature
import proxy 内部包。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [服务入口开发](../aifd-rules/skills/service-entrypoint-development/SKILL.md)
- [账号身份与存储工作流](../aifd-rules/skills/account-identity-store-workflow/SKILL.md)
- [Web 客户端](web/README.md)
