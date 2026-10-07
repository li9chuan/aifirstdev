package contracts

// MessageID 是业务项目分配的稳定协议 ID。
type MessageID uint32

// Direction 描述消息在后端中的流向。
type Direction string

const (
	DirectionClientToServer Direction = "client_to_server"
	DirectionServerToClient Direction = "server_to_client"
	DirectionServerToServer Direction = "server_to_server"
	DirectionAdmin          Direction = "admin"
)

// FieldDescriptor 只记录兼容性检查需要的字段元数据。
type FieldDescriptor struct {
	Number   int
	Name     string
	Type     string
	Repeated bool
	Optional bool
}

// MessageDescriptor 是框架中立的 proto 消息描述；真实 .proto 文件和生成 pb 包由业务项目拥有。
type MessageDescriptor struct {
	Name       string
	ID         MessageID
	Direction  Direction
	ProtoFile  string
	GoType     string
	Deprecated bool
	Fields     []FieldDescriptor
}

// Clone 返回可安全交给 registry 和 validator 使用的描述副本。
func (d MessageDescriptor) Clone() MessageDescriptor {
	out := d
	if len(d.Fields) > 0 {
		out.Fields = append([]FieldDescriptor(nil), d.Fields...)
	}
	return out
}

// FieldByNumber 按 proto 字段编号返回字段。
func (d MessageDescriptor) FieldByNumber(number int) (FieldDescriptor, bool) {
	for _, field := range d.Fields {
		if field.Number == number {
			return field, true
		}
	}
	return FieldDescriptor{}, false
}
