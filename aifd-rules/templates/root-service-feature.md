# Root / Service Feature 模板

使用流程见[Feature 开发 skill](../skills/feature-development/SKILL.md)，生命周期依据见
[runtime 组织原则](../../docs/scope-runtime.md)，可运行接线示例见[minigame](../../aifd-examples/minigame/README.md)。
这里提供目录与职责提示，按功能需要选择文件，不要求创建全部空文件。

建议文件：

```text
features/<name>/
  feature.go
  root.go
  service.go
  rpc.go
  storage.go
  config.go
  events.go
  robot/
```

root module 只拥有当前业务根文档中的状态；service module 拥有跨 root 的索引、
聚合、投递、关系或任务状态。业务项目可按领域拆分文件名，但不要把 game/world
这类示例业务词汇作为框架默认入口。

## 相关文档

- [规则与资料入口](../README.md)
