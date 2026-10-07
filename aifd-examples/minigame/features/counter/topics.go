package counter

const (
	// TopicMceCounterAdd 是客户端请求增加计数器的协议名。
	TopicMceCounterAdd = "MceCounterAdd"
	// TopicMseCounterState 是服务端返回计数器状态的协议名。
	TopicMseCounterState = "MseCounterState"
	// TopicGlobalCounterAdd 是 game host 通知 world host 统计计数器变化的内部 RPC。
	TopicGlobalCounterAdd = "Counter.GlobalAdd"
)

const (
	// ConfigTableCounter 是示例 counter 的配置表名。
	ConfigTableCounter = "Counter"
	// ConfigRowDefault 是示例 counter 的默认配置行。
	ConfigRowDefault = "default"
	// ConfigFieldLimit 是 counter 上限字段。
	ConfigFieldLimit = "limit"
)
