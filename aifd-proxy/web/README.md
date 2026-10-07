# @aifd/proxy-client

浏览器端 AIFD proxy 客户端。包内拥有通用 `MsgHead` 生成绑定、packet codec、WebSocket
生命周期、await inbox 与回包匹配；业务项目只提供自己的 protobuf schema、topic 和业务 DTO。

```text
npm ci
npm run generate
npm test
npm run build
```

`MsgHead` 的唯一事实源是 `aifd-schema/proto/aifd/proxy/proxy.proto`。不要手改
`src/gen` 下的生成文件。

## 相关文档

- [Proxy 模块入口](../README.md)
- [Go 客户端](../../aifd-client/README.md)
- [Schema 与 Proto 工作流](../../aifd-rules/skills/schema-proto-workflow/SKILL.md)
