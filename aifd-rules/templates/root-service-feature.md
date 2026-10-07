# Root / Service Feature 模板

建议文件：

```text
features/<name>/
  feature.go
  root.go
  service.go
  rpc.go
  storage.go
  events.go
```

root module 只拥有当前业务根文档中的状态；service module 拥有跨 root 的索引、
聚合、投递、关系或任务状态。业务项目可按领域拆分文件名，但不要把 game/world
这类示例业务词汇作为框架默认入口。
