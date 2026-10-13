# @aifd/proxy-client

浏览器端 AIFD proxy 客户端。包内拥有通用 `MsgHead` 生成绑定、packet codec、WebSocket
生命周期、await inbox 与回包匹配；业务项目只提供自己的 protobuf schema、topic 和业务 DTO。

```text
npm ci
npm run generate
npm test
npm run build
```

`MsgHead` 和 `RPCError` 的唯一事实源是 `aifd-schema/proto/aifd/proxy/proxy.proto`。不要手改
`src/gen` 下的生成文件。

`client.call(topic, pbType, body)` 的 Promise 在正常回包时 resolve `Packet`，即使 body
为空。匹配 inbox 的 `RPCError` 回包会 reject 导出的 `ProxyRPCError`；`error.code`
是服务端错误码，`error.message` 是原消息。空错误码默认为 `INTERNAL`，旧服务端的
空错误包返回 `INTERNAL / rpc request failed`。损坏的非空错误负载会使所有等待请求
以协议解码错误 reject 并关闭连接，不会留下未结束的等待。推送和过期回包仍交由
`onPush` / `onStaleReply` 处理。

## 相关文档

- [Proxy 模块入口](../README.md)
- [Go 客户端](../../aifd-client/README.md)
- [Schema 与 Proto 工作流](../../aifd-rules/skills/schema-proto-workflow/SKILL.md)
