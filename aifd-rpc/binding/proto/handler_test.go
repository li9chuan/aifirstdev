package proto

import (
	"context"
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestHandlerWithUIDAndProtoResponse(t *testing.T) {
	handler, err := Handler(func(uid int64, req *wrapperspb.StringValue) *wrapperspb.StringValue {
		if uid != 7 {
			t.Fatalf("uid = %d, want 7", uid)
		}
		return wrapperspb.String(req.Value + ":rsp")
	})
	if err != nil {
		t.Fatalf("Handler error = %v", err)
	}
	req, err := rpc.NewMessagePB("Req", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	var responses []rpc.Message
	session := rpcruntime.NewSession(rpcruntime.SessionOptions{
		UID: 7, Topic: "Req",
		Respond: func(_ context.Context, msg rpc.Message) error {
			responses = append(responses, msg)
			return nil
		},
	})
	if err := handler(context.Background(), session, req); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1", len(responses))
	}
	var rsp wrapperspb.StringValue
	if err := responses[0].DecodePB(&rsp); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	if rsp.Value != "hello:rsp" {
		t.Fatalf("rsp = %s, want hello:rsp", rsp.Value)
	}
}

func TestHandlerRejectsUnsupportedSignature(t *testing.T) {
	if _, err := Handler(func(float64) {}); err == nil {
		t.Fatal("Handler unsupported signature error = nil")
	}
}
