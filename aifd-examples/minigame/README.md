# minigame

## 定位

`minigame` 是一个完整游戏后端框架示例，用于展示业务服务、proxy、account、runtime、
rdb、config、observe、robot 和 split/all-in-one 入口如何组合成游戏后端框架链路。
它不是完整玩法内容或真实线上运营系统。
它按业务领域命名自己的 `game/world` 服务，内部组合框架的 `runtime/user` 和 `runtime/service`。
游戏里的 `DBPlayer` 只是业务 proto 名。

## 负责

- `account demo login -> MAuthPayload + token -> proxy LoginHandler -> Game.AuthOK -> MseAuth` 登录流程。
- `MceCounterAdd -> game user module -> DBPlayer -> MseCounterState` 业务功能接入方式。
- counter world service module 的恢复、启动、内部 RPC 和独立 world 统计状态。
- 内存配置快照、validator、observe memory recorder 的业务接入方式。
- 业务项目自己的 `proto/`、`generated/pbmsg/`、`features/auth/` 和 `cmd/` 组织方式。
- 本地 all-in-one WebSocket demo，使用 local RPC bus 和 memory rdb，不依赖 Redis/NATS。
- 普通业务 client，用 `aifd-client` 连接 proxy，登录后发起一次 counter await 请求。
- robot client，用 `aifd-robot` 通过真实 WebSocket 连接 proxy，登录后执行一次 counter 请求。
- proxy-broker split 拓扑：proxy 可显式嵌入 NATS broker；game/world 只作为 NATS
  client 连接该 broker 集群。

## 不负责

- 完整生产玩法逻辑、运营数据或真实服务部署治理。
- 背包、竞技场、复杂玩法 robot、压测行为树或真实 Luban 表文件。
- 真实业务项目的登录协议字段、线上账号 provider、线上 key 轮换或运营登录页面。
- 应放在 `aifd-runtime`、`aifd-rpc`、`aifd-rdb`、`aifd-schema` 或
  `aifd-config` 中的框架抽象。

## 业务项目如何使用

业务项目可以复制这里的目录分层：业务 proto 放在项目自己的 `proto/`，生成物放在项目
自己的 `generated/pbmsg/`。`DBPlayer` 也是业务 proto，不进入框架层；账号注册、provider
绑定和 payload token 签发由 `aifd-account` 负责，proxy 只本地验签，game host 的登录确认放在
`features/auth/game`。
具体业务功能放在 `features/counter` 这样的 feature 目录里，由业务自己决定协议名、
配置表名、Redis key、指标名和 DBPlayer 子字段。

客户端先通过账号中心拿到 `MAuthPayload + token`，再用 `MsgHead.topic = MceAuth`
连接 proxy；proxy 验签后把同一个 `MceAuth` 转发给 game host。服务端登录回包使用
`MsgHead.topic = MseAuth`，只包含 `ret`。登录身份和 new 标记写入 `DBPlayer.auth`。
`pbType` 是 body 的 protobuf 类型名，不等同于 topic。
登录后的业务请求使用 `MsgHead.topic = MceCounterAdd`，服务端返回
`MsgHead.topic = MseCounterState`。

可以先启动本地 all-in-one 后端；它在一个进程里组装 proxy、game host、world host、
local RPC、memory rdb、memory config 和 memory observe：

```powershell
go run ./cmd/minigame
```

再启动普通 client：

```powershell
go run ./cmd/client -uid 1001 -counter-delta 1
```

也可以启动 robot client 做自动化场景验证：

```powershell
go run ./cmd/robot -run-num 1 -uid-start 1001 -counter-delta 1
```

如果要看拆分服务入口，默认使用 proxy-broker：proxy 进程先启动 embedded NATS broker，
game/world 作为 NATS client 连接 `[rpc.nats].servers` 中的 proxy-broker TCP 地址。

```powershell
go run ./cmd/proxy --conf ./configs/service.example.toml --addr 127.0.0.1:18080
go run ./cmd/world --conf ./configs/service.example.toml
go run ./cmd/game --conf ./configs/service.example.toml
```

`cmd/world` 只组 service lifecycle 和 `Counter.GlobalAdd` route；`cmd/game` 只组 game
业务 route 和 user runtime；`cmd/proxy` 只组 WebSocket proxy、token verifier、
LoginHandler、RPC bus 和可选 embedded broker；`cmd/minigame` 才是 all-in-one demo。
`--addr`、`--pprof` 和 `--metrics` 这类实例参数仍由命令行提供；共享配置文件只放 log、
NATS、Redis、proxy 和 proxy-broker 行为配置。`cmd/game` 和 `cmd/world` 可用 `--nats`
覆盖要连接的 proxy-broker TCP client 地址。

当前 split 示例的 game/world 仍显式使用 `NewMemoryDB`；配置中的 Redis 字段不会自动切换持久化后端。
需要跨重启保存时，在业务入口根据 [RDB 用法](../../aifd-rdb/README.md)显式注入 Redis DB，不把 split 运行当作持久化部署验收。

`configs/service.example.toml` 是单 proxy 本地默认配置，已经启用 proxy-broker：

```toml
[proxy_broker]
enabled = true
config_file = "./configs/nats/proxy-local.conf"
ready_timeout = "10s"
shutdown_timeout = "10s"
```

`cmd/proxy` 在 `proxy_broker.enabled=true` 时先启动 embedded broker，再让 proxy RPC bus
使用 in-process NATS 连接；game/world 仍通过 `[rpc.nats].servers` 连接 proxy-broker
集群的 TCP client 地址。`configs/nats/proxy-local.conf` 是本地单 proxy 默认配置；
`proxy-a.conf`、`proxy-b.conf`、`proxy-c.conf` 展示三台 proxy 的 NATS 原生 cluster
routes 写法。多机生产必须把 `listen` 和 `routes` 改成节点间可路由地址，并为每个
proxy 使用各自的 `proxy_broker.config_file`，例如通过独立配置文件或
`AIFD_PROXY_BROKER_CONFIG_FILE` 覆盖。broker 端 auth、TLS、JetStream store 等参数继续
使用原生 NATS server 配置表达；game/world client 端在 `[rpc.nats]` 里配置 `servers`、
`credentials_file`、`tls_ca_file`、`tls_cert_file` 和 `tls_key_file`。

## 更新边界

这个示例可以直接跟随框架 API 变化。它用于展示完整游戏后端框架链路；新增能力应优先补齐
`rpc/rdb/runtime/proxy/robot/config/observe/schema/account` 的边界和接线方式，而不是扩展玩法深度。

## 相关文档

- [示例入口](../README.md)
- [业务 Feature 开发](../../aifd-rules/skills/feature-development/SKILL.md)
- [服务入口开发](../../aifd-rules/skills/service-entrypoint-development/SKILL.md)
