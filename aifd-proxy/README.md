# aifd-proxy

## 定位

`aifd-proxy` 定义客户端网关边界：认证连接、session 路由、消息投递，以及进入或
离开后端的流量 adapter 契约。

## 负责

- Connection 和 session 契约。
- Gateway 投递和路由原语。
- Local、TCP 和 websocket adapter 形态。
- WebSocket binary frame 接入、登录状态、重复登录策略和断线清理。
- 客户端 wire protocol 对 `aifd.schema` 中通用 PB 帧头语义的使用。
- 可选 proxy-broker 入口能力：`adapters/nats/broker` 只封装 embedded NATS server
  生命周期，供 proxy 进程显式组合。

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
字段语义参考现有项目的 `MsgHead`：`len` 是 body 长度，`inbox` 是 RPC 回调编号，
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

WebSocket adapter 使用 `github.com/coder/websocket`，只接受 binary frame。业务项目
需要提供 `LoginHandler`，由它解析登录 PB、校验账号中心签发的 signed token，并返回
`Identity` 和可选登录回包。proxy 在登录成功后才把连接注册进 `Gateway`，并订阅该
uid 的下行消息。
未登录连接只接受登录 topic；如果业务启用心跳，也应配置为 `McePing` / `MsePong`
这类业务协议名，并且只在登录成功后由 proxy 本地回复。

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

需要更细的规则时，实现 `DuplicateHandler`。断线时可以实现 `SessionLifecycle`，由业务
自行决定是否通知 game/runtime offline；proxy 不写死 offline RPC。

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

## 更新边界

Proxy 升级应保持 session 和 delivery 契约。adapter 变更不能要求业务 feature
import proxy 内部包。
