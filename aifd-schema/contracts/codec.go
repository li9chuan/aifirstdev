package contracts

// Codec 序列化类型化消息，但不强制绑定具体 proto runtime。
type Codec interface {
	Name() string
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
}
