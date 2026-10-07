package json

import stdjson "encoding/json"

// Codec 是测试和工具用 codec；真实业务项目可以接入 protobuf 实现而不改变 schema 契约。
type Codec struct{}

// Name 返回 codec 名称。
func (Codec) Name() string {
	return "json"
}

// Marshal 将类型化消息编码为 JSON。
func (Codec) Marshal(value any) ([]byte, error) {
	return stdjson.Marshal(value)
}

// Unmarshal 从 JSON 解码类型化消息。
func (Codec) Unmarshal(data []byte, value any) error {
	return stdjson.Unmarshal(data, value)
}
