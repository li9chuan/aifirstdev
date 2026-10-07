package contracts

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
)

const (
	// HeaderUID 在 transport 间携带请求 uid。
	HeaderUID = "uid"
	// HeaderClientInbox 携带 proxy 投递使用的客户端 callback inbox。
	HeaderClientInbox = "s"
	// HeaderResponse 携带下行响应 topic 或推送消息名。
	HeaderResponse = "rsp"
	// HeaderPBType 携带 payload 对应的 protobuf 结构名。
	HeaderPBType = "pbType"
	// HeaderStatus 携带 transport 层错误状态，并与 NATS header 对齐。
	HeaderStatus = "Status"
	// HeaderErrorMessage 携带框架错误的人类可读描述。
	HeaderErrorMessage = "errorMessage"
	// HeaderTraceID 预留给跨服务 tracing。
	HeaderTraceID = "traceID"
)

var (
	ErrNilProtoMessage = errors.New("rpc proto message is nil")
	ErrEmptyMessage    = errors.New("rpc message is empty")
)

// Message 是 transport 中立的 RPC 信封。
type Message struct {
	Name    string
	Payload []byte
	Header  map[string]string
}

// Clone 返回可安全放入异步队列和 callback cache 的副本。
func (m Message) Clone() Message {
	out := m
	if len(m.Payload) > 0 {
		out.Payload = append([]byte(nil), m.Payload...)
	}
	if len(m.Header) > 0 {
		out.Header = make(map[string]string, len(m.Header))
		for key, value := range m.Header {
			out.Header[key] = value
		}
	}
	return out
}

// NewMessagePB 将 protobuf 消息 marshal 成 transport 中立信封。
// 如果 topic 为空，则使用 protobuf descriptor 名称作为默认 topic。
func NewMessagePB(topic string, pb proto.Message) (Message, error) {
	if pb == nil {
		return Message{}, ErrNilProtoMessage
	}
	pbType := string(pb.ProtoReflect().Descriptor().Name())
	if topic == "" {
		topic = pbType
	}
	payload, err := proto.Marshal(pb)
	if err != nil {
		return Message{}, fmt.Errorf("rpc marshal %s: %w", topic, err)
	}
	return Message{
		Name:    topic,
		Payload: payload,
		Header:  map[string]string{HeaderPBType: pbType},
	}, nil
}

// DecodePB 将消息 payload unmarshal 到 dst。
func (m Message) DecodePB(dst proto.Message) error {
	if dst == nil {
		return ErrNilProtoMessage
	}
	if err := proto.Unmarshal(m.Payload, dst); err != nil {
		return fmt.Errorf("rpc decode %s: %w", m.Name, err)
	}
	return nil
}

// ProtoName 返回 protobuf descriptor 名称。
func ProtoName(pb proto.Message) (string, error) {
	if pb == nil {
		return "", ErrNilProtoMessage
	}
	return string(pb.ProtoReflect().Descriptor().Name()), nil
}
