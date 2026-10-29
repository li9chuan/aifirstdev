# 原生 HTTPS/WSS 入口

配置与完整生命周期由 [httpserver 文档](../../../../aifd-proxy/adapters/httpserver/README.md)维护。
跨模块职责、状态与存储取舍见[设计说明](../../../../docs/native-https-design.md)；业务端共同接线见[业务接入](../../../../docs/application-integration.md)。

- 使用 `httpserver.Start` 显式组合标准 HTTP server 和既有 WS handler；plain 缺省，manual/acme 显式启用。
- TLS 与证书状态属于 proxy 入口适配器；业务协议、账号与权限不进入证书组件，runtime 不复制 ACME 状态机。
- ACME 首版单机单进程、明确域名、TLS-ALPN-01；不要为业务接入重新实现签发、续期或下载 shell 脚本。
- Start 成功不是证书已就绪；结合 transport.Ready 与业务自身准备状态，不把 pending 或 expired 标成 Ready。
- 续期失败且旧证书仍有效时保留服务并报告 degraded；禁止通过回退明文或关闭校验来掩盖失败。
- 证书缓存是私有恢复事实源，位于版本目录之外，区分生产与测试；账户、密钥和证书组合必须在重启后保持可用。
- key/cert/metadata 使用完整 bundle 原子提交；不要改回三文件分别覆盖，否则库回滚可能删除旧私钥。
- ARI 会单独更新 metadata；必须保留对应的 key/cert 原子落盘，不得与正在暂存的替代证书混配。
- 所有 ACME 请求复用私有冷却 transport；429/503 `Retry-After` 和失败退避持久保存，不能因重启、调用库内部重试或 ARI 绕过。测试按真实请求计数验证，CA 全局配额仍由 CA 判定。
- 关闭时结束 WS 并释放租约，限时关闭 HTTP，取消 ACME 并等待持锁任务收尾；库的维护循环停止不代表其异步任务已退出。
- WSS 客户端复用 proxyws 的 RequireTLS、HTTPClient 与重定向策略，不修改进程全局 HTTPClient 或手工跳过证书校验。
- 验收轮换必须观察磁盘与新的完整 TLS 握手序列号，不能只模拟成功回调；本地 CA 通过不能冒充公网 CA 部署验收。
