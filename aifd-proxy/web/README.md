# @aifd/proxy-client

浏览器端 AIFD proxy 客户端。包内拥有通用 `MsgHead` 生成绑定、packet codec、WebSocket
生命周期、await inbox 与回包匹配；业务项目只提供自己的 protobuf schema、topic 和业务 DTO。

## 构建与消费

包当前标记 `private: true`，包名不代表已发布到 npm。从业务锁定的 AIFD 源码版本构建，
再由业务构建系统以本地包/workspace 等方式消费 `dist`，不依赖浮动 main。在本目录执行：

```text
npm ci
npm run generate
npm test
npm run build
```

`MsgHead` 和 `RPCError` 的唯一事实源是 `aifd-schema/proto/aifd/proxy/proxy.proto`。不要手改
`src/gen` 下的生成文件。
仅使用已提交绑定时可跳过 generate；修改 proto 后需重建。generate 会使用 `AIFD_PROTOC` 或安装仓库锁定的 protoc 到本地工具缓存。

## 连接与请求

完成本地包映射后，业务提供已序列化的 PB bytes：

```ts
import { ProxyWebSocketClient } from "@aifd/proxy-client";

const client = await ProxyWebSocketClient.connect("wss://api.example.com/ws", {
  onPush: handleBusinessPush,
});
const reply = await client.call("MceAuth", "MceAuth", authBody);
// Decode and validate the application's login response before business calls.
// Close the client when its owner exits: client.close().
```

`call` 是有超时的请求/回包；`send(topic, pbType, body)` 是 inbox=0 的单向发送。
`call` 可传单次 timeout 和 AbortSignal，取消会清除对应等待。默认连接和请求超时均为 10 秒。
没有自动重连、请求重放或离线 push 存储，重连后由业务重新鉴权并读取事实。
该 Web SDK 没有 Go transport 的 `RequireTLS` 字段；公网由应用使用 WSS，交由浏览器正常校验证书。

登录成功后可 `startHeartbeat({ probe, onLost })`，Probe 接收 AbortSignal 并校验强类型业务回包。
默认间隔 30 秒、超时 10 秒、连续失败 2 次通知失联；`stopHeartbeat/close` 主动取消不报失联。
不把 Proxy 本地 Pong 当作业务服务存活证据；它也不续期 Runtime 连接租约，续租 Probe 使用业务心跳或 `MceSessionHeartbeat`。

`client.call(topic, pbType, body)` 的 Promise 在正常回包时 resolve `Packet`，即使 body
为空。匹配 inbox 的 `RPCError` 回包会 reject 导出的 `ProxyRPCError`；`error.code`
是服务端错误码，`error.message` 是原消息。空错误码默认为 `INTERNAL`，旧服务端的
空错误包返回 `INTERNAL / rpc request failed`。损坏的非空错误负载会使所有等待请求
以协议解码错误 reject 并关闭连接，不会留下未结束的等待。推送和过期回包仍交由
`onPush` / `onStaleReply` 处理。

完整签名与事件语义见 [client.ts](src/client.ts)，packet 实现见 [codec.ts](src/codec.ts)。

## 相关文档

- [Proxy 模块入口](../README.md)
- [Go 客户端](../../aifd-client/README.md)
- [Schema 与 Proto 工作流](../../aifd-rules/skills/schema-proto-workflow/SKILL.md)
