package codec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

const webClientPacketGolden = "0803107b1a104d7365496e76656e746f7279496e666f220b537472696e6756616c7565010203"

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
