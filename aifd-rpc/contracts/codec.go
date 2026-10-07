package contracts

// Codec 将类型化消息映射为 transport 字节。
type Codec interface {
	Name() string
	Encode(any) ([]byte, error)
	Decode([]byte, any) error
}
