# 独立凭据验证

本包及渠道子包不依赖 Account 根包、accountpb、aifd-auth 或 Runtime。Verifier.Verify
只返回可信 Principal{AuthorityID, Subject}；主体只能来自服务端验证，不能来自客户端声明。
Mux 按登记的 ConnectorID 选择验证器。

## 手机验证码

phoneotp.NewProvider(sender, store, options) 由宿主注入短信供应商 Sender 和共享挑战存储。
RequestCode 先原子占用手机号冷却窗口，再发送一次；明确成功后才激活挑战。
失败或结果不明不自动重发、不释放冷却窗口。默认有效期 5 分钟、冷却 1 分钟、最多 5 次尝试。
号码使用带国家码的规范格式；验证码只存 HMAC 摘要，临时存储中的号码仍是敏感数据。

客户端提交 EncodeProof(connectorID, Proof{ChallengeID, Code})；验证精确比较用途和 scope，
计数与一次性消费必须原子执行。MemoryStore 是单进程参照实现；多实例需实现共享 Store，
保留冷却、激活、尝试上限、过期及并发消费语义。本包没有预置短信供应商或生产 Redis 挑战存储。

## 微信网页与小程序

wechat.NewHTTPClient 向微信官方 HTTPS API 交换 code，设置超时、限制响应体并拒绝重定向。默认及标准 HTTP Transport 使用独立、不复用连接的 HTTP/1 传输，避免一次性 code 在响应丢失后被隐式重试；宿主自定义 RoundTripper 必须同样禁止结果不明后的重放。
不返回 access_token、session_key 或 AppSecret；网络错误不包含带密钥的 URL。

- 网页：NewWebsiteProvider + BeginAuthorization 创建一次性 state/挑战，回调地址从配置白名单选择。
  EncodeWebsiteProof 带回 challenge ID、code 和 state；state/用途/scope 通过后先消费挑战再交换 code，
  上游结果不明必须重新开始授权。
- 小程序：NewMiniProgramProvider 接收 EncodeMiniProgramProof 中的 code，用配置 appid/secret
  调用 jscode2session。它没有预创建挑战，不把 Binding 当作 code 来源证明。
- SubjectOpenID 使用应用独立 authority；SubjectUnionID 仅在确认同属一个微信开放平台身份空间时共享
  authority，缺少 UnionID 直接拒绝，不退回 OpenID，不自动合并账号。

MemoryWebsiteStore 同样是参照实现；共享部署需注入原子一次性消费的 WebsiteStore。
宿主完成客户端 UI、回调路由、应用配置及 actor 授权；凭据包不决定注册政策或业务准入。

协议依据：[网站应用微信登录](https://developers.weixin.qq.com/doc/oplatform/Website_App/WeChat_Login/Wechat_Login.html)、
[小程序 code2Session](https://developers.weixin.qq.com/miniprogram/dev/OpenApiDoc/user-login/code2Session.html)。
当前本地 HTTP 测试验证请求、响应及失败边界，不等同于真实微信应用或短信送达验收。
