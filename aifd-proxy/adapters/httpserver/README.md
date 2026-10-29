# 原生 HTTP、HTTPS 与 WSS

`httpserver` 是可选的公开 HTTP 入口适配器。它显式启动一个标准 Go HTTP server，
复用现有 mux 和 WebSocket handler。HTTPS/WSS 直接由进程提供，不依赖 Nginx、
Caddy、Certbot 或 shell 续期任务。证书管理使用固定版本 CertMagic，ACME 请求使用 acmez Go 库。

自 [v0.6.0](../../../docs/releases/v0.6.0.md) 提供。跨模块接入见[业务接入](../../../docs/application-integration.md)，
职责、运行模型与取舍见[设计说明](../../../docs/native-https-design.md)。

## 接入

```go
base := entrypoint.NewHTTPServer(mux)
base.Addr = ":443"
transport, err := httpserver.Start(ctx, httpserver.Config{
    HTTPServer: base,
    TLS: httpserver.TLSConfig{
        Mode: httpserver.ModeACME,
        Domains: []string{"api.example.com"},
        CacheDir: "/var/lib/example-service/tls",
        ACMEEnvironment: "production",
        AgreeTerms: true,
    },
    CloseWebSockets: proxy.Close,
})
if err != nil { return err }
defer transport.Close(context.Background())
<-transport.Done()
return transport.Err()
```

`HTTPServer` 必须是尚未运行、明确指定 Addr/Handler 的实例，Start 后其所有权移交给适配器。
不得从其他 goroutine 修改其 Handler、TLSConfig 或调用 Serve。已有 HTTP 连接超时保留；
缺省 ReadHeaderTimeout 为 5 秒、MaxHeaderBytes 为 1 MiB。业务请求体与文件下载的限制由 handler 拥有。
一个 mux 可同时承载登录引导、业务 WS、管理 WS、下载和健康检查，路由及身份校验仍属于接入项目。

存在 WebSocket 时必须提供 `CloseWebSockets`，例如既有 `proxy.Close`。多个 WS handler
由入口组合关闭。回调必须遵守 context；它不应等待某个已持有的业务锁。
标准 `http.Server.Shutdown` 不管理 hijacked WebSocket。

## 配置契约

`TLSConfig` 可以嵌入宿主的 typed 文件配置；`TLSConfigEnvKeys("http.tls")` 返回可供宿主注册的键。
本包不自行读取环境变量、全局配置或业务配置文件。

```toml
[http]
addr = ":443"

[http.tls]
mode = "acme"
domains = ["api.example.com"]
cache_dir = "/var/lib/example-service/tls"
acme_environment = "production"
agree_terms = true
```

| 模式 | 配置与行为 |
|---|---|
| 缺省 / `plain` | 保留 HTTP/WS；不得携带证书或 ACME 字段 |
| `manual` | 必须有 domains、cert_file、key_file；使用含完整证书链的 PEM，校验有效期、域名与密钥匹配 |
| `acme` | 必须有 domains、cache_dir、agree_terms；自动申请与续期；environment 缺省 production，另支持 staging |

域名输入列表最多 100 项，规范化大小写、IDNA 和尾点后去重。ACME 由库按名称独立管理，不合并为一张 100 SAN 证书。
配置不接受通配符、IP、localhost 或单标签名称；manual 的 PEM 可以包含通配符，但 domains 仍列出允许访问的具体名称。
首版没有 DNS-01、多进程共享缓存或 HA 协调。SNI 和 HTTP Host 必须属于已配置名称，未知名称不会触发签发。
manual 模式更换证书后重启；域名、CA 环境和存储位置等配置变更也通过重启生效。

## ACME 部署条件

- 域名 A/AAAA 必须指向正确服务；错误 AAAA 也可能导致多地点验证失败。
- 公网 TCP 443 必须能到达该 TLS listener，并保留 `acme-tls/1` ALPN。
- 默认只使用 TLS-ALPN-01，不监听 80、不写 DNS 记录，也不修改云安全组。
- 可直接监听 443，也可将公网 443 通过 TCP 端口映射到配置的高位端口；仅暴露高位端口不满足验证条件。
- 进程需要出站访问 CA，系统时间应准确。Linux 绑定 443 可由 systemd 授予
  `CAP_NET_BIND_SERVICE`；本包不要求业务进程以 root 运行。
- 部署者显式接受 CA 协议；组件自动创建和恢复 ACME 账户，不需要网站账号或付款。

先用 staging 验证域名及端口。staging 证书不受公开客户端信任，不能关闭证书校验来冒充生产验收。
生产只配置 Let's Encrypt 单一 issuer，不自动转到其他 CA。
失败重试保持所选 CA 环境，不自动切换到 staging 或其他 CA。

## 状态、恢复与关闭

Start 先校验并绑定监听，再启动证书管理。成功返回表示 listener 已启动，首次 ACME 可能仍处于 pending。
`Ready()` 表示全部配置名称都有当前有效证书；应用自身的准备状态还应与它组合。
`Status()` 是无网络副作用的快照，包含 mode、state、ready、last error code、每个名称的
有效期和十进制序列号，不返回私钥或可重用授权 URL。
这是 Go 对象查询 API，不会自动注册状态路由。首次签发前或证书过期时，同一公网 HTTPS 探针也可能握手失败；
如需查询 pending/expired，由宿主在已有内部控制面组合 Status，不关闭证书校验。

| 状态 | Ready 与行为 |
|---|---|
| pending | false；继续 ACME 验证及库的退避重试，业务握手拒绝 |
| ready | true；有效证书正常服务 |
| degraded | true；签发/续期/落盘失败但现有证书仍有效，日志与 status 报告错误 |
| expired | false；没有可用替代证书，新的业务握手拒绝，包括会话恢复路径 |
| stopping / stopped | false；停止接入并释放资源 |

续期使用库的 ARI 和实际证书寿命策略，不硬编码证书天数。新证书保存并载入后用于新的完整 TLS
握手，已有 WSS 连接不因轮换断开。TLS session resumption 可保留原有 peer certificate 元数据，
因此验收新证书必须使用禁用会话恢复的完整握手。

缓存以 `<cache_dir>/<environment>` 隔离测试与生产，包含 ACME 账户和已提交证书资产。
私有目录在 Unix 要求 0700，文件以 0600 原子写入；Windows 设置服务用户、SYSTEM 与管理员的私有 DACL。
缓存必须位于版本发布目录之外，不能在升级、回滚或退出时清理，也不能进入 Git、产品包或公开静态目录。

证书的 key/cert/metadata 在内部映射到一个版本化 `.aifd-bundle` 原子提交文件：
提交前崩溃或续期写盘失败不会覆盖上一套完整资产；库的失败回滚也不能删除旧私钥。
ARI 单独更新 metadata 时也原子提交完整 bundle，保留原 key/cert 及 CA 给出的下次查询时间。
以 ACME 证书资源 URL 区分旧证书信息刷新与新证书提交，避免刷新旧 ARI 时提交尚在暂存的新 key/cert。
bundle 是持久事实源，未完成写入只存在于内存。账户及其密钥以一个完整文件原子保存，注册前先持久化密钥身份；
注册中断后复用该身份重试。账户、冷却状态和库的签发锁使用同一个私有 FileStorage。
损坏或读失败不同于缓存不存在，恢复失败会明确拒绝启动，不删除资产或伪造 Ready。

Close/父 context 取消后，先停止接入，调用 WS 关闭回调，再限时排空 HTTP；超时会强制关闭 HTTP 并返回错误。
ACME 请求和等待被取消，证书维护 cache 显式停止，并等待已接纳的持锁任务完成收尾写入和解锁。
`Done` 后读取 `Err` 判断完整退出结果。
关闭超时或错误必须记录，不能只凭 Done 就宣布所有底层清理已成功完成。
ACME 库日志不直接转发，适配器只记录白名单结果，避免带出 challenge URL、账户响应、私钥和内部资产路径。

## CA 限流处理的当前边界

适配器在 CertMagic 的公开 Issuer 接口中组合 acmez 客户端，所有 ACME 请求经过专用 transport，
包括库内部重试和 ARI 请求，不修改全局 HTTP client 或依赖库的私有字段。

- HTTP 429/503 支持秒数和 HTTP 日期两种 `Retry-After`。同一 CA 的后续请求等待至服务器要求时间与本地退避时间的较晚者，已在途请求可以完成。
- 网络失败和签发失败使用本地退避，起始 30 秒、逐次翻倍，上限 6 小时；服务器要求更长等待时不缩短。
  CA 请求失败按 CA 暂停，其他签发失败按证书名称暂停。
- 冷却截止时间和失败次数原子写入私有缓存，重启后继续等待。损坏或不可读的已有状态拒绝启动；
  冷却写盘失败后停止进一步 CA 请求，修复存储并重启后恢复。取消等待不会丢弃已收到的冷却信息。
- 账户/证书复用、环境隔离和 ARI 仍保留；ARI 正常查询的下次时间随 metadata 保存，续期订单携带替代证书信息。
- [CA 全局配额](https://letsencrypt.org/docs/rate-limits/)仍由 CA 判定。本组件不维护全局配额账本，也不协调其他进程或机器上的 ACME 客户端。
  部署时不要通过清缓存、换账户或频繁重启来重试签发。

测试覆盖 429/503、两种响应头格式、库内部请求路径、重启冷却、存储故障及取消关闭；
本地 CA 测试不替代公开 CA 的部署验收。

## 客户端

```go
transport, err := proxyws.NewWebSocketTransport(ctx, proxyws.WebSocketConfig{
    URL: "wss://api.example.com/ws",
    RequireTLS: true,
    DialTimeout: 5 * time.Second,
})
```

客户端使用系统根证书；可选 HTTPClient 用于受控代理或测试 CA，不会被原地修改。
标准 HTTP transport 拒绝 InsecureSkipVerify，并确保 TLS 1.2 以上。
安全初始 URL 拒绝降级和跨源重定向，策略在调用者 redirect hook 之前与之后都检查。
自定义 RoundTripper 属于受信任调用方的适配入口，由调用方保证它执行真实 TLS 和证书校验。
浏览器使用标准 HTTPS/WSS 信任与同源机制；PB、认证、inbox、心跳和 Push 不变。

## 验证

默认单测不连接公开 CA。真实 ACME 测试使用本地 Pebble、私有测试 CA、DNS fixture 和随机高位端口。
测试显式设置 `PEBBLE_VA_ALWAYS_VALID=0`，不绕过域名验证；Pebble 不进入运行时依赖。

```sh
go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1
export AIFD_PEBBLE_BIN="$(go env GOPATH)/bin/pebble"
go test -race -tags=integration ./aifd-proxy/adapters/httpserver ./aifd-client/adapters/proxyws -count=1
go test ./...
go mod verify
```

Windows 将 AIFD_PEBBLE_BIN 设置为对应 pebble.exe；运行 race 需要启用 CGO 和可用的 C 编译器。
未设置该变量时，带 integration tag 的 Pebble 用例明确 Skip，不能把 Skip 当作集成通过。

分批门禁：

1. 原生监听：HTTP/HTTPS/WSS、证书信任/域名/过期、Host/SNI、旧 TLS 拒绝、并发关闭。
2. ACME：真实 TLS-ALPN 签发，后台自动续期，完整握手和磁盘序列号更新，旧 WSS 保持连接。
3. 故障恢复：CA 离线时有效缓存恢复；续期落盘失败仍可重启恢复；损坏缓存拒绝；pending 取消与关闭超时。
4. 消费接入：Go SDK PB 包往返、禁止明文/降级/跨源、调用者 hook 不得绕过策略，以及可运行示例。

2026-10-29 分批本地验收记录（Windows amd64，Go 1.27.3）：

| 批次 | 结果 |
|---|---|
| 1 原生监听 | 通过：HTTP/HTTPS/WSS、错误证书/域名/Host/SNI、过期、旧协议及并发关闭 |
| 2 自动证书 | 通过：Pebble 真实 TLS-ALPN 验证与后台自动续期，证书在磁盘和完整握手中更新，原 WSS 连接保持；真实 ARI 刷新后下次查询时间持久保存 |
| 3 故障恢复 | 通过：续期提交失败后旧组合仍可离线重启；损坏缓存拒绝；在途 ACME 取消、HTTP 关闭超时及 Windows 私有 ACL |
| 4 接入 | 通过：Go SDK PB 往返、URL/Host 重定向策略、HTTP/2 共存，以及示例 readiness/WS echo 冒烟 |

根模块 `go test ./...`、上述带 integration tag 的 race 测试、`go mod verify` 与文档链接/skill 结构检查通过。
额外通过 Linux amd64 的适配器测试二进制交叉编译和示例交叉构建；该结果不代表 Linux 运行期验收。

公开域名的 Let's Encrypt staging、公网证书链、网络多地点可达性和长期生产续期仍需在实际部署环境验收。
本地 Pebble 通过不能替代这些部署证据。

## 相关文档

- [运行示例](../../../aifd-examples/native-https/README.md)
- [Proxy](../../README.md)
- [入口开发规则](../../../aifd-rules/skills/service-entrypoint-development/references/native-https.md)
- [Let's Encrypt challenge types](https://letsencrypt.org/docs/challenge-types/)
- [CertMagic](https://pkg.go.dev/github.com/caddyserver/certmagic)
