package nats

import (
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	natsgo "github.com/nats-io/nats.go"
)

func TestMessageFromNATSUsesStructuredHeaders(t *testing.T) {
	nmsg := &natsgo.Msg{
		Data: []byte{1, 2},
		Header: natsgo.Header{
			rpc.HeaderResponse:     []string{"MseInventoryInfo"},
			rpc.HeaderPBType:       []string{"StringValue"},
			rpc.HeaderClientInbox:  []string{"123"},
			rpc.HeaderErrorMessage: []string{"ignored by name"},
			rpc.HeaderTraceID:      []string{"trace-1"},
		},
	}
	msg := messageFromNATS(nmsg)
	if msg.Name != "MseInventoryInfo" {
		t.Fatalf("name = %q, want MseInventoryInfo", msg.Name)
	}
	if msg.Header[rpc.HeaderPBType] != "StringValue" {
		t.Fatalf("pbType = %q, want StringValue", msg.Header[rpc.HeaderPBType])
	}
	if msg.Header[rpc.HeaderClientInbox] != "123" {
		t.Fatalf("client inbox = %q, want 123", msg.Header[rpc.HeaderClientInbox])
	}
}

func TestMessageFromNATSFallsBackToPBType(t *testing.T) {
	nmsg := &natsgo.Msg{
		Data:   []byte{1},
		Header: natsgo.Header{rpc.HeaderPBType: []string{"MsePong"}},
	}
	msg := messageFromNATS(nmsg)
	if msg.Name != "MsePong" {
		t.Fatalf("name = %q, want MsePong", msg.Name)
	}
}
