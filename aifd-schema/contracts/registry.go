package contracts

// Registry 存储 schema 描述，不 import 生成的 proto 代码。
type Registry interface {
	Register(MessageDescriptor) error
	GetByName(string) (MessageDescriptor, bool)
	GetByID(MessageID) (MessageDescriptor, bool)
	Messages() []MessageDescriptor
}
