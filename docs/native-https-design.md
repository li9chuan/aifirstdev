# 原生 HTTPS/WSS 设计

本页说明当前源码的职责与运行模型。配置和接入代码只在[httpserver README](../aifd-proxy/adapters/httpserver/README.md)维护，
可运行入口见[native-https 示例](../aifd-examples/native-https/README.md)，发布范围与限制见 [v0.6.0](releases/v0.6.0.md)。

## 目标与职责

让业务显式选择 plain、manual 或 ACME，在同一个 Go HTTP server 上复用 HTTP/WS handler，
无需额外反向代理或 shell 续期任务。TLS 保护传输；业务登录、授权和消息幂等由原有业务层负责。

| 拥有方 | 内容 |
|---|---|
| `aifd-proxy/adapters/httpserver` | listener、TLS、证书可用性、ACME 管理、私有缓存和关闭 |
| 宿主入口 / Runtime wiring | typed 配置、依赖顺序、业务就绪状态、WS 关闭回调和信号退出 |
| 业务 handler / 客户端 | 路由、身份与权限、公开 URL、正常证书校验、重连后鉴权和事实恢复 |

Runtime 不拥有证书或 ACME 状态机；也不把自动证书放进账号中心。HTTP 与 WSS 共用 TLS，
WebSocket 升级后的 PB/packet、inbox 和 RPC 语义保持原职责。

## 启动、运行与退出

1. 校验配置和明确域名，绑定 listener；TLS 配置由适配器拥有。
2. manual 加载匹配域名、密钥和有效期的 PEM；ACME 先恢复持久账户与证书，再启动异步管理。
   损坏或不可读的已有证书不当作空缓存处理。
3. `Start` 返回表示接入已启动。`Ready/Status` 根据实际有效证书计算，宿主还要组合自己的业务 readiness。
   所有配置域名均须有有效证书；未知 SNI/Host 不触发动态签发。
4. 后台按 ARI/实际证书寿命续期。提交并载入新证书后，新的完整 TLS 握手使用新证书，已有 WSS 继续运行。
   续期失败而旧证书仍有效时可以继续服务并报告 degraded；过期后拒绝新的业务握手。
5. 退出时取消管理、停止接入，关闭 WS，再限时排空 HTTP，停止证书维护。宿主继续关闭其业务依赖。

首次签发前只允许必要的 ACME challenge 握手，公共 HTTPS readiness 路径也可能无法访问。
宿主可从已有内部控制面读取无网络副作用的 `Status()`；组件不自动增加明文管理端口。
manual 证书文件没有热重载，更换文件后重启。证书轮换验收须使用新的完整握手，会话恢复可能保留旧 peer certificate 元数据。

## 资产与故障边界

`<cache_dir>/<environment>` 保存账户与证书，独立于版本目录，staging/production 隔离。
Unix 使用私有目录和文件权限，Windows 限定 DACL；升级、回滚或退出均不清空缓存。

证书 key、cert 和 metadata 映射到一个版本化 bundle 原子提交，提交失败保留旧组合。
ARI 可独立更新 metadata，按证书资源 URL 识别对应组合，并保留 CA 的下次查询时间。
账户、冷却状态与锁使用同一私有存储。账户密钥在注册前原子保存，注册中断后复用该身份重试；
冷却状态写盘失败时停止进一步 CA 请求。这些机制不构成多进程 HA 承诺。

## CA 请求与冷却

CertMagic 负责缓存、ARI 调度和续期计划；适配器通过公开 Issuer 接口组合 acmez，
让每个 ACME HTTP 请求都经过实例私有的冷却 transport，包括库内部重试。
429/503 的 `Retry-After` 与本地失败退避取较晚的截止时间，在返回该响应前原子保存；重启不重置等待。
CA 请求失败按 CA 暂停，其他签发失败按证书名称暂停。已有在途请求可完成；取消等待不清空冷却记录。
私有缓存拥有截止时间和失败次数，Runtime 与业务不复制这些状态，也不增加业务配置。

退出先取消请求与等待，停止新持锁任务和维护循环，再等待已接纳任务完成收尾写入及解锁。
依赖库的维护循环停止不等于其异步任务已退出，因此由存储锁生命周期补齐等待。

实现入口是 [server.go](../aifd-proxy/adapters/httpserver/server.go)、[acme.go](../aifd-proxy/adapters/httpserver/acme.go)
和 [assets.go](../aifd-proxy/adapters/httpserver/assets.go)。请求冷却见 [acme_retry.go](../aifd-proxy/adapters/httpserver/acme_retry.go)，
Issuer 接入见 [acme_issuer.go](../aifd-proxy/adapters/httpserver/acme_issuer.go)，任务关闭见 [acme_storage.go](../aifd-proxy/adapters/httpserver/acme_storage.go)。
ACME 库日志不直接转发，适配器发布有限的状态与错误码。

## 当前取舍与未完成项

- ACME 固定使用 Let's Encrypt，支持明确 DNS 名和 TLS-ALPN-01；公网 443 必须到达该 listener。
  每个名称由库独立管理，不把配置域名数量上限当作官方注册域/账户配额控制。
- 无 DNS-01、通配符域名配置、IP 自动证书、HA、自动修改 DNS/安全组或 manual 文件热重载。
- 已有账户复用、证书恢复、ARI、429/503 `Retry-After` 和跨重启冷却；不维护跨实例的官方配额账本。
  完整现状在[限流边界](../aifd-proxy/adapters/httpserver/README.md#ca-限流处理的当前边界)维护。
- 当前不提供 HTTP→HTTPS 跳转端口、HSTS 策略或自动改写业务下载 URL；有需求时由宿主显式组合。

## 验证入口

[单元测试](../aifd-proxy/adapters/httpserver/server_test.go)覆盖配置、TLS/Host/SNI、状态和退出；
[资产测试](../aifd-proxy/adapters/httpserver/assets_test.go)覆盖中断写入、ARI 元数据更新与失败恢复；
[冷却测试](../aifd-proxy/adapters/httpserver/acme_retry_test.go)与[接入测试](../aifd-proxy/adapters/httpserver/acme_issuer_test.go)覆盖真实 HTTP 响应、重启和取消；
[Pebble 集成](../aifd-proxy/adapters/httpserver/acme_integration_test.go)覆盖真实 TLS-ALPN 签发、自动续期、ARI、WSS 保持及离线恢复。
[客户端测试](../aifd-client/adapters/proxyws/tls_test.go)覆盖 WSS 与重定向策略。

命令与已验证环境见[验证说明](../aifd-proxy/adapters/httpserver/README.md#验证)。本地 Pebble 和 Windows 测试
不能替代公开域名 staging、实际证书链、多地点网络可达性和长期生产续期验收。
