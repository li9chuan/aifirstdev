# 原生 HTTPS/WSS 示例

此示例在框架根 Go module 内运行，无独立 go.mod。它展示 HTTP/HTTPS、`/readyz` 和 `/ws` echo
如何共享同一原生入口；真实业务的身份和授权由业务项目自己的 handler 提供。
使用 [v0.6.0](../../docs/releases/v0.6.0.md) 引入的入口能力。

在仓库根运行本地明文示例：

```sh
go run ./aifd-examples/native-https -addr 127.0.0.1:19084
curl http://127.0.0.1:19084/readyz
```

已有证书：

```sh
go run ./aifd-examples/native-https -addr :443 -mode manual \
  -domains api.example.com -cert /secure/fullchain.pem -key /secure/key.pem
```

自动申请免费证书，先验证 staging：

```sh
go run ./aifd-examples/native-https -addr :443 -mode acme \
  -domains api.example.com -cache-dir /var/lib/example-service/tls \
  -acme-environment staging -agree-terms
```

将域名替换为自己控制且已正确解析的名称，并允许公网 443 到达该进程。成功后将
`-acme-environment` 改为 `production` 重启。staging 与 production 缓存隔离。
生产客户端使用 `https://api.example.com` 和 `wss://api.example.com/ws`；首次签发完成前 Ready 为 false。
在 pending 状态下，同一 HTTPS `/readyz` 也可能无法完成 TLS 握手，此时查看进程状态日志。
Ctrl+C 取消进程 context，入口会关闭 WS、HTTP 和证书维护任务。

Linux 绑定 443 需要受控的低位端口权限或 TCP 端口映射；这些是部署方配置，本示例不会自动修改系统。
无需安装 Nginx、Caddy、Certbot，不需要 DNS API 密钥或购买证书。

完整配置、持久化和测试命令见[原生入口文档](../../aifd-proxy/adapters/httpserver/README.md)。
