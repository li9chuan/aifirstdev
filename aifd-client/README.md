# aifd-client

## 定位

`aifd-client` 是客户端侧 protobuf 调用门面，供 robot、Wails Go 后端和其他模拟客户端复用。

## 使用方式

出站统一使用 `Call`：

- `client.Call(req)`：只发送，不等待回包。
- `client.Call(req, func(rsp *Rsp, err error) { ... })`：发送并异步等待回包。

入站 push 使用 `On`：

```go
client.On(func(msg *pbmsg.Push) {
	// update local client state
})
```

wire topic 从 protobuf message 名称推导，例如 `*pbmsg.MceCounterAdd` 会发送到
`MceCounterAdd`。业务层不需要重复传 topic。

调用方只需要提供 `Transport`，不需要处理 `rpc.Message`、`HeaderClientInbox`、callback cache 或回包匹配。

`Options.AwaitTimeout` 只负责清理未回包的 inbox；网络拨号、读写和关闭超时由具体 `Transport` 自己处理。

需要接收 push 或使用带 callback 的 await `Call` 前，必须先调用 `Start()` 启动读取和超时清理循环；未启动时 await `Call` 会返回 `ErrNotStarted` 且不会发送请求。无 callback 的 `Call` 只做单向发送，不要求先 `Start()`。

## Proxy WebSocket

`adapters/proxyws` 提供连接 `aifd-proxy` 的 WebSocket transport：

```go
transport, err := proxyws.NewWebSocketTransport(ctx, proxyws.WebSocketConfig{
	URL:         "ws://127.0.0.1:18080/ws",
	DialTimeout: time.Second,
})
cli, err := aifdclient.New(transport, aifdclient.Options{AwaitTimeout: 10 * time.Second})
```

该 adapter 只负责 WebSocket 和 proxy packet 编解码，不理解业务登录、robot 场景或 runtime 服务。

`Request(ctx, req, rsp)` 与 `RequestAs(ctx, topic, req, rsp)` 同步等待回包。
`Session.Error` 的错误回包会返回 `*rpc.StatusError`，其 `Code` 和 `Message` 保留服务端错误，
不解码业务响应。`Call/CallAs` 的远端失败通过 callback 报告：`rsp == nil`，`err != nil`；
`Call` 的直接返回值仍表示本地发送结果。这些 await 调用均需先调用 `Start()`。

空 `RPCError` 包返回 `INTERNAL / rpc request failed`。错误码和消息由服务端 codec
编码在 RPCError PB 中，客户端在业务解码前识别。损坏的非空 `RPCError` body 按
协议解码失败结束连接和等待请求；普通的空 protobuf 成功回包仍然成功。

认证成功后，`StartHeartbeat(HeartbeatOptions{Probe, OnLost})` 调度串行、有期限的
端到端 Probe。业务用自己的强类型 Request 校验响应；无业务心跳可使用
`MceSessionHeartbeat/MseSessionHeartbeat`。默认间隔 30 秒、超时 10 秒、连续失败
2 次通知失联。Go Probe 必须遵守 Context，调度器不会强杀不合作的回调。
StopHeartbeat 和显式 Close 取消当前 Probe，不报失联；异常断连或连续失败最多
触发一次 OnLost，回调可安全 Close。Web SDK 提供相同语义及 AbortSignal。

inbox 为 0 的 UID 事件通过 `On/OnAs` 独立消费，不完成待返回的 Call。不同设备
可以使用相同 inbox；响应仍只回原连接。重连重新读取业务事实，不从在线 Push
历史推断离线同步。详见 [业务调用规则](../aifd-rules/skills/feature-development/references/rpc-user-and-multidevice.md)。

## 相关文档

- [项目首页](../README.zh-CN.md)
- [Proxy 接入与协议](../aifd-proxy/README.md)
- [桌面应用开发](../aifd-rules/skills/desktop-app-development/SKILL.md)
- [项目与 Feature 代码评审](../aifd-rules/skills/runtime-feature-review/SKILL.md)
