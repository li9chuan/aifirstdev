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
