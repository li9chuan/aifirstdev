package auth

const (
	// TopicMceAuth 是客户端登录请求协议名。
	TopicMceAuth = "MceAuth"
	// TopicMseAuth 是服务端登录响应协议名。
	TopicMseAuth = "MseAuth"
	// TopicGameAuthOK 是 proxy 调 game host 的内部登录确认 RPC。
	TopicGameAuthOK = "Game.AuthOK"
)

const (
	// RetOK 表示登录成功。
	RetOK int32 = 0
	// RetInvalidAuth 表示登录请求或 signed token 无效。
	RetInvalidAuth int32 = 1
	// RetGameFailed 表示 game 登录流程失败。
	RetGameFailed int32 = 2
)
