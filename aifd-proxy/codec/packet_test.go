package codec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/proto"
)

const webClientPacketGolden = "0803107b1a104d7365496e76656e746f7279496e666f220b537472696e6756616c7565010203"

// The Web codec test uses the same packet: INVALID / 请求无效 / inbox 123.
const webClientRPCErrorGolden = "0817107b1a085250434572726f7222085250434572726f720a07494e56414c4944120ce8afb7e6b182e697a0e69588"

func TestTransportStatusWithRequestNameBecomesRPCError(t *testing.T) {
	parts, err := EncodeMessage(rpc.Message{Name: "MceSessionHeartbeat", Header: map[string]string{rpc.HeaderStatus: "503", rpc.HeaderClientInbox: "9"}})
	if err != nil {
		t.Fatal(err)
	}
	head, body, err := DecodePacket(bytes.Join(parts, nil))
	if err != nil {
		t.Fatal(err)
	}
	if head.Topic != "RPCError" || head.PbType != "RPCError" || head.Inbox != 9 {
		t.Fatalf("transport error lost: %v", head)
	}
	msg, err := MessageFromPacket(head, body)
	if err != nil {
		t.Fatal(err)
	}
	status, ok := rpc.StatusFromMessage(msg).(*rpc.StatusError)
	if !ok || status.Code != "503" {
		t.Fatalf("status=%v", status)
	}
}

func TestRPCErrorMessagePacketRoundTrip(t *testing.T) {
	msg := rpc.Message{
		Name:    "RPCError",
		Payload: []byte{9}, // Error headers, rather than an existing payload, own the status.
		Header: map[string]string{
			rpc.HeaderStatus:       "INVALID",
			rpc.HeaderErrorMessage: "请求无效",
			rpc.HeaderClientInbox:  "123",
			rpc.HeaderPBType:       "StringValue",
		},
	}
	parts, err := EncodeMessage(msg)
	if err != nil {
		t.Fatalf("EncodeMessage error = %v", err)
	}
	data := bytes.Join(parts, nil)
	if got := hex.EncodeToString(data); got != webClientRPCErrorGolden {
		t.Fatalf("error packet golden = %s, want %s", got, webClientRPCErrorGolden)
	}
	head, body, err := DecodePacket(data)
	if err != nil {
		t.Fatalf("DecodePacket error = %v", err)
	}
	restored, err := MessageFromPacket(head, body)
	if err != nil {
		t.Fatalf("MessageFromPacket error = %v", err)
	}
	status, ok := rpc.StatusFromMessage(restored).(*rpc.StatusError)
	if !ok || status.Code != "INVALID" || status.Message != "请求无效" {
		t.Fatalf("restored status = %v, want INVALID / 请求无效", status)
	}
	if head.PbType != "RPCError" || restored.Header[rpc.HeaderClientInbox] != "123" {
		t.Fatalf("restored packet = %+v, want RPCError / inbox 123", restored)
	}
	if msg.Header[rpc.HeaderPBType] != "StringValue" || !bytes.Equal(msg.Payload, []byte{9}) {
		t.Fatalf("EncodeMessage changed the source message: %+v", msg)
	}
}

func TestRPCErrorPacketRejectsMalformedPayload(t *testing.T) {
	if _, err := MessageFromPacket(&proxypb.MsgHead{Inbox: 7, Topic: "RPCError", PbType: "RPCError"}, []byte{0xff}); err == nil {
		t.Fatal("MessageFromPacket accepted a malformed RPCError payload")
	}
}

func TestRPCErrorEmptyCodeDefaultsToInternal(t *testing.T) {
	// Encoding also supports the existing rsp topic fallback.
	parts, err := EncodeMessage(rpc.Message{Header: map[string]string{
		rpc.HeaderResponse:     "RPCError",
		rpc.HeaderErrorMessage: "服务不可用",
		rpc.HeaderClientInbox:  "7",
	}})
	if err != nil {
		t.Fatal(err)
	}
	head, body, err := DecodePacket(bytes.Join(parts, nil))
	if err != nil {
		t.Fatal(err)
	}
	status := &proxypb.RPCError{}
	if err := proto.Unmarshal(body, status); err != nil || status.Code != "INTERNAL" || status.Message != "服务不可用" {
		t.Fatalf("encoded status = %+v, %v", status, err)
	}
	// A peer can also send a nonempty body whose code was omitted.
	body, err = proto.Marshal(&proxypb.RPCError{Message: "服务不可用"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := MessageFromPacket(head, body)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header[rpc.HeaderStatus] != "INTERNAL" || msg.Header[rpc.HeaderErrorMessage] != "服务不可用" || msg.Header[rpc.HeaderClientInbox] != "7" {
		t.Fatalf("decoded status = %+v", msg)
	}
}

func TestEncodeDecodePacketWithTopicAndPBType(t *testing.T) {
	body := []byte{1, 2, 3}
	parts, err := EncodePacket(&proxypb.MsgHead{
		Inbox:  123,
		Topic:  "MseInventoryInfo",
		PbType: "StringValue",
	}, body)
	if err != nil {
		t.Fatalf("EncodePacket error = %v", err)
	}
	data := bytes.Join(parts, nil)
	if got := hex.EncodeToString(data); got != webClientPacketGolden {
		t.Fatalf("packet golden = %s, want %s", got, webClientPacketGolden)
	}
	head, decodedBody, err := DecodePacket(data)
	if err != nil {
		t.Fatalf("DecodePacket error = %v", err)
	}
	if head.Inbox != 123 || head.Topic != "MseInventoryInfo" || head.PbType != "StringValue" {
		t.Fatalf("head = %+v", head)
	}
	if !bytes.Equal(decodedBody, body) {
		t.Fatalf("body = %v, want %v", decodedBody, body)
	}
}

func TestPacketFallbacksKeepTopicAndPBTypeStructured(t *testing.T) {
	parts, err := EncodePacket(&proxypb.MsgHead{Inbox: 7, PbType: "StringValue"}, []byte{9})
	if err != nil {
		t.Fatalf("EncodePacket pbType fallback error = %v", err)
	}
	head, _, err := DecodePacket(bytes.Join(parts, nil))
	if err != nil {
		t.Fatalf("DecodePacket pbType fallback error = %v", err)
	}
	if head.Topic != "StringValue" || head.PbType != "StringValue" {
		t.Fatalf("head = %+v, want topic and pbType StringValue", head)
	}

	parts, err = EncodePacket(&proxypb.MsgHead{Topic: "McePing"}, []byte{1})
	if err != nil {
		t.Fatalf("EncodePacket topic fallback error = %v", err)
	}
	head, _, err = DecodePacket(bytes.Join(parts, nil))
	if err != nil {
		t.Fatalf("DecodePacket topic fallback error = %v", err)
	}
	if head.Topic != "McePing" || head.PbType != "McePing" {
		t.Fatalf("head = %+v, want topic and pbType McePing", head)
	}
}

func TestMessagePacketConversion(t *testing.T) {
	msg, err := MessageFromPacket(&proxypb.MsgHead{Inbox: 123, Topic: "MceInventoryInfo", PbType: "StringValue"}, []byte{1, 2})
	if err != nil {
		t.Fatalf("MessageFromPacket error = %v", err)
	}
	if msg.Name != "MceInventoryInfo" {
		t.Fatalf("msg name = %q, want MceInventoryInfo", msg.Name)
	}
	if msg.Header[rpc.HeaderClientInbox] != "123" {
		t.Fatalf("client inbox = %q, want 123", msg.Header[rpc.HeaderClientInbox])
	}
	if msg.Header[rpc.HeaderPBType] != "StringValue" {
		t.Fatalf("pbType = %q, want StringValue", msg.Header[rpc.HeaderPBType])
	}

	head, err := HeadFromMessage(rpc.Message{
		Name:    "MseInventoryInfo",
		Payload: []byte{3, 4},
		Header: map[string]string{
			rpc.HeaderClientInbox: "123",
			rpc.HeaderPBType:      "StringValue",
		},
	})
	if err != nil {
		t.Fatalf("HeadFromMessage error = %v", err)
	}
	if head.Inbox != 123 || head.Topic != "MseInventoryInfo" || head.PbType != "StringValue" || head.Len != 2 {
		t.Fatalf("head = %+v", head)
	}
}

func TestDecodePacketRejectsInvalidBodyLength(t *testing.T) {
	parts, err := EncodePacket(&proxypb.MsgHead{Topic: "McePing"}, []byte{1, 2})
	if err != nil {
		t.Fatalf("EncodePacket error = %v", err)
	}
	data := append(bytes.Join(parts, nil), 3)
	if _, _, err := DecodePacket(data); !errors.Is(err, ErrInvalidBody) {
		t.Fatalf("DecodePacket err = %v, want ErrInvalidBody", err)
	}
}

func TestHeadFromMessageRejectsInvalidInbox(t *testing.T) {
	_, err := HeadFromMessage(rpc.Message{
		Name:   "MsePong",
		Header: map[string]string{rpc.HeaderClientInbox: "bad"},
	})
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("HeadFromMessage err = %v, want ErrInvalidPacket", err)
	}
}

func TestValidateClientTopic(t *testing.T) {
	valid := []string{"MceAuth", "McwGuildInfo"}
	for _, topic := range valid {
		if err := ValidateClientTopic(topic); err != nil {
			t.Fatalf("ValidateClientTopic(%q) error = %v", topic, err)
		}
		if !IsClientTopic(topic) {
			t.Fatalf("IsClientTopic(%q) = false", topic)
		}
	}

	invalid := []string{"Game.AuthOK", "C.MceAuth", "MceAuth1", "Mce_Auth", "mceAuth", "Mc", ""}
	for _, topic := range invalid {
		if err := ValidateClientTopic(topic); !errors.Is(err, ErrInvalidPacket) {
			t.Fatalf("ValidateClientTopic(%q) error = %v, want ErrInvalidPacket", topic, err)
		}
		if IsClientTopic(topic) {
			t.Fatalf("IsClientTopic(%q) = true", topic)
		}
	}
}
