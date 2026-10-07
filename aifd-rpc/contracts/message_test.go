package contracts

import (
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNewMessagePBAndDecodePB(t *testing.T) {
	msg, err := NewMessagePB("", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	if msg.Name != "StringValue" {
		t.Fatalf("Name = %s, want StringValue", msg.Name)
	}
	if msg.Header[HeaderPBType] != "StringValue" {
		t.Fatalf("pbType = %s, want StringValue", msg.Header[HeaderPBType])
	}

	var out wrapperspb.StringValue
	if err := msg.DecodePB(&out); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	if out.Value != "hello" {
		t.Fatalf("Value = %s, want hello", out.Value)
	}
}

func TestNewMessagePBSeparatesTopicAndPBType(t *testing.T) {
	msg, err := NewMessagePB("MseInventoryInfo", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	if msg.Name != "MseInventoryInfo" {
		t.Fatalf("Name = %s, want MseInventoryInfo", msg.Name)
	}
	if msg.Header[HeaderPBType] != "StringValue" {
		t.Fatalf("pbType = %s, want StringValue", msg.Header[HeaderPBType])
	}
}

func TestMessageCloneDoesNotShareHeaderOrPayload(t *testing.T) {
	msg := Message{
		Name:    "Test",
		Payload: []byte{1, 2, 3},
		Header:  map[string]string{"a": "b"},
	}
	clone := msg.Clone()
	clone.Payload[0] = 9
	clone.Header["a"] = "c"

	if msg.Payload[0] != 1 {
		t.Fatalf("payload shared")
	}
	if msg.Header["a"] != "b" {
		t.Fatalf("header shared")
	}
}

func TestNewMessagePBRejectsNil(t *testing.T) {
	if _, err := NewMessagePB("Nil", nil); err == nil {
		t.Fatal("NewMessagePB nil error = nil")
	}
}

func TestDecodePBAllowsEmptyProtoPayload(t *testing.T) {
	msg, err := NewMessagePB("Empty", &emptypb.Empty{})
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	if len(msg.Payload) != 0 {
		t.Fatalf("payload length = %d, want empty protobuf bytes", len(msg.Payload))
	}
	var out emptypb.Empty
	if err := msg.DecodePB(&out); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
}
