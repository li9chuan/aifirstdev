package codec

import (
	"errors"
	"fmt"
	"strconv"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/encoding/protowire"
	goproto "google.golang.org/protobuf/proto"
)

var (
	// ErrInvalidPacket 表示 packet 头部缺失或字段组合非法。
	ErrInvalidPacket = errors.New("invalid proxy packet")
	// ErrInvalidBody 表示 MsgHead.len 与实际 body 长度不一致。
	ErrInvalidBody = errors.New("invalid proxy packet body length")
)

// EncodePacket 将 MsgHead 和 body 编码为 protobuf(MsgHead)+body 的线格式。
func EncodePacket(head *proxypb.MsgHead, body []byte) ([][]byte, error) {
	normalized, err := normalizeHead(head, len(body))
	if err != nil {
		return nil, err
	}
	headBytes, err := goproto.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return [][]byte{headBytes}, nil
	}
	return [][]byte{headBytes, append([]byte(nil), body...)}, nil
}

// DecodePacket 从 protobuf(MsgHead)+body 的线格式解出结构化头和 body。
func DecodePacket(data []byte) (*proxypb.MsgHead, []byte, error) {
	if len(data) == 0 {
		return nil, nil, ErrInvalidPacket
	}
	return decodePacket(data)
}

// MessageFromPacket 将 packet 转成 rpc.Message，并还原 RPCError 的错误 Header。
func MessageFromPacket(head *proxypb.MsgHead, body []byte) (rpc.Message, error) {
	normalized, err := normalizeHead(head, len(body))
	if err != nil {
		return rpc.Message{}, err
	}
	header := map[string]string{}
	if normalized.PbType != "" {
		header[rpc.HeaderPBType] = normalized.PbType
	}
	if normalized.Inbox > 0 {
		header[rpc.HeaderClientInbox] = strconv.Itoa(int(normalized.Inbox))
	}
	if normalized.Topic == "RPCError" && len(body) > 0 {
		status := &proxypb.RPCError{}
		if err := goproto.Unmarshal(body, status); err != nil {
			return rpc.Message{}, fmt.Errorf("decode RPCError: %w", err)
		}
		code := status.Code
		if code == "" {
			code = "INTERNAL"
		}
		header[rpc.HeaderStatus] = code
		header[rpc.HeaderErrorMessage] = status.Message
	}
	return rpc.Message{
		Name:    normalized.Topic,
		Payload: append([]byte(nil), body...),
		Header:  header,
	}, nil
}

// HeadFromMessage 将 RPC 下行消息转成 MsgHead，供客户端 packet 编码使用。
func HeadFromMessage(msg rpc.Message) (*proxypb.MsgHead, error) {
	topic := msg.Name
	if topic == "" && len(msg.Header) > 0 {
		topic = msg.Header[rpc.HeaderResponse]
	}
	pbType := ""
	if len(msg.Header) > 0 {
		pbType = msg.Header[rpc.HeaderPBType]
	}
	if pbType == "" {
		pbType = topic
	}
	if msg.Header[rpc.HeaderStatus] != "" {
		topic, pbType = "RPCError", "RPCError"
	}
	inbox, err := parseInbox(msg.Header)
	if err != nil {
		return nil, err
	}
	return normalizeHead(&proxypb.MsgHead{
		Len:    int32(len(msg.Payload)),
		Inbox:  int32(inbox),
		Topic:  topic,
		PbType: pbType,
	}, len(msg.Payload))
}

// EncodeMessage 将 RPC 下行消息编码为 packet；RPCError body 由错误 Header 生成。
func EncodeMessage(msg rpc.Message) ([][]byte, error) {
	head, err := HeadFromMessage(msg)
	if err != nil {
		return nil, err
	}
	body := msg.Payload
	if head.Topic == "RPCError" {
		code := msg.Header[rpc.HeaderStatus]
		if code == "" {
			code = "INTERNAL"
		}
		body, err = goproto.Marshal(&proxypb.RPCError{Code: code, Message: msg.Header[rpc.HeaderErrorMessage]})
		if err != nil {
			return nil, fmt.Errorf("encode RPCError: %w", err)
		}
		head.PbType = "RPCError"
	}
	return EncodePacket(head, body)
}

func decodePacket(data []byte) (*proxypb.MsgHead, []byte, error) {
	head := &proxypb.MsgHead{}
	offset := 0
	lastField := protowire.Number(0)
	seenField := map[protowire.Number]bool{}

	for offset < len(data) {
		fieldStart := offset
		fieldNum, wireType, tagLen := protowire.ConsumeTag(data[offset:])
		if tagLen < 0 {
			return nil, nil, protowire.ParseError(tagLen)
		}
		if fieldNum < 1 || fieldNum > 4 || seenField[fieldNum] || (lastField != 0 && fieldNum < lastField) {
			offset = fieldStart
			break
		}

		offset += tagLen
		switch fieldNum {
		case 1:
			if wireType != protowire.VarintType {
				return nil, nil, ErrInvalidPacket
			}
			value, valueLen := protowire.ConsumeVarint(data[offset:])
			if valueLen < 0 {
				return nil, nil, protowire.ParseError(valueLen)
			}
			head.Len = int32(value)
			offset += valueLen
		case 2:
			if wireType != protowire.VarintType {
				return nil, nil, ErrInvalidPacket
			}
			value, valueLen := protowire.ConsumeVarint(data[offset:])
			if valueLen < 0 {
				return nil, nil, protowire.ParseError(valueLen)
			}
			head.Inbox = int32(value)
			offset += valueLen
		case 3:
			if wireType != protowire.BytesType {
				return nil, nil, ErrInvalidPacket
			}
			value, valueLen := protowire.ConsumeString(data[offset:])
			if valueLen < 0 {
				return nil, nil, protowire.ParseError(valueLen)
			}
			head.Topic = value
			offset += valueLen
		case 4:
			if wireType != protowire.BytesType {
				return nil, nil, ErrInvalidPacket
			}
			value, valueLen := protowire.ConsumeString(data[offset:])
			if valueLen < 0 {
				return nil, nil, protowire.ParseError(valueLen)
			}
			head.PbType = value
			offset += valueLen
			seenField[fieldNum] = true
			lastField = fieldNum
			goto parsed
		}

		seenField[fieldNum] = true
		lastField = fieldNum
	}

parsed:
	bodyLen := int(head.Len)
	if bodyLen < 0 {
		return nil, nil, ErrInvalidBody
	}
	normalized, err := normalizeHead(head, bodyLen)
	if err != nil {
		return nil, nil, err
	}
	head = normalized
	if bodyLen == 0 {
		if offset != len(data) {
			return nil, nil, ErrInvalidBody
		}
		return head, nil, nil
	}
	if len(data)-offset != bodyLen {
		headerLen := len(data) - bodyLen
		if headerLen <= 0 {
			return nil, nil, ErrInvalidBody
		}
		verifyHead := &proxypb.MsgHead{}
		if err := goproto.Unmarshal(data[:headerLen], verifyHead); err != nil {
			return nil, nil, ErrInvalidBody
		}
		normalized, err := normalizeHead(verifyHead, bodyLen)
		if err != nil {
			return nil, nil, ErrInvalidBody
		}
		verifyHead = normalized
		if int(verifyHead.Len) != bodyLen {
			return nil, nil, ErrInvalidBody
		}
		return verifyHead, append([]byte(nil), data[headerLen:]...), nil
	}
	return head, append([]byte(nil), data[offset:]...), nil
}

func normalizeHead(head *proxypb.MsgHead, bodyLen int) (*proxypb.MsgHead, error) {
	if head == nil {
		return nil, ErrInvalidPacket
	}
	if bodyLen < 0 {
		return nil, ErrInvalidBody
	}
	out := goproto.Clone(head).(*proxypb.MsgHead)
	out.Len = int32(bodyLen)
	if out.Topic == "" && out.PbType != "" {
		out.Topic = out.PbType
	}
	if bodyLen > 0 && out.PbType == "" {
		out.PbType = out.Topic
	}
	if bodyLen > 0 && out.PbType == "" {
		return nil, ErrInvalidPacket
	}
	if out.Topic == "" && (out.Inbox <= 0 || out.PbType == "") {
		return nil, ErrInvalidPacket
	}
	return out, nil
}

func parseInbox(header map[string]string) (int, error) {
	if len(header) == 0 || header[rpc.HeaderClientInbox] == "" {
		return 0, nil
	}
	inbox, err := strconv.Atoi(header[rpc.HeaderClientInbox])
	if err != nil || inbox < 0 {
		return 0, fmt.Errorf("%w: invalid inbox %q", ErrInvalidPacket, header[rpc.HeaderClientInbox])
	}
	return inbox, nil
}
