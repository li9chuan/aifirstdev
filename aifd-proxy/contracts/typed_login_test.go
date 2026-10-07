package contracts

import (
	"context"
	"errors"
	"testing"

	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNewLoginHandlerDefersInvalidDefaultLoginTopicToValidate(t *testing.T) {
	handler := NewLoginHandler(func(context.Context, LoginContext, *wrapperspb.StringValue) (Identity, *wrapperspb.StringValue) {
		return Identity{UID: 1001}, wrapperspb.String("ok")
	})
	if handler == nil {
		t.Fatal("NewLoginHandler = nil")
	}
	if err := handler.Validate(); !errors.Is(err, proxycodec.ErrInvalidPacket) {
		t.Fatalf("Validate error = %v, want invalid client topic", err)
	}
}

func TestNewTypedLoginHandlerDefersNilHandlerToValidate(t *testing.T) {
	handler := NewTypedLoginHandler(TypedLoginOptions[*wrapperspb.StringValue, *wrapperspb.StringValue]{
		LoginTopic: "MceAuth",
	})
	if handler == nil {
		t.Fatal("NewTypedLoginHandler = nil")
	}
	if err := handler.Validate(); !errors.Is(err, ErrInvalidTypedLoginHandler) {
		t.Fatalf("Validate error = %v, want invalid typed login handler", err)
	}
}

func TestTypedLoginHandlerUsesExplicitLoginTopicAndDefaultResultTopic(t *testing.T) {
	handler := NewTypedLoginHandler(TypedLoginOptions[*wrapperspb.StringValue, *wrapperspb.StringValue]{
		LoginTopic: "MceAuth",
		Handler: func(context.Context, LoginContext, *wrapperspb.StringValue) (Identity, *wrapperspb.StringValue) {
			return Identity{UID: 1001}, wrapperspb.String("ok")
		},
	})
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
	if handler.Topic() != "MceAuth" {
		t.Fatalf("Topic = %q, want MceAuth", handler.Topic())
	}
	if handler.ResultTopic() != "StringValue" {
		t.Fatalf("ResultTopic = %q, want StringValue", handler.ResultTopic())
	}

	msg, err := rpc.NewMessagePB("MceAuth", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	result, err := handler.Login(context.Background(), LoginRequest{ConnectionID: "conn-1", Message: msg})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if result.Identity.UID != 1001 || result.CloseAfterResponse {
		t.Fatalf("result = %+v, want successful identity", result)
	}
	if result.Response == nil || result.Response.Name != "StringValue" {
		t.Fatalf("response = %+v, want StringValue topic", result.Response)
	}
}

func TestTypedLoginHandlerDecodeFailureUsesFailureResponse(t *testing.T) {
	called := false
	handler := NewTypedLoginHandler(TypedLoginOptions[*wrapperspb.StringValue, *wrapperspb.StringValue]{
		LoginTopic: "MceAuth",
		Handler: func(context.Context, LoginContext, *wrapperspb.StringValue) (Identity, *wrapperspb.StringValue) {
			called = true
			return Identity{UID: 1001}, wrapperspb.String("ok")
		},
		DecodeFailure: func(context.Context, LoginContext, error) *wrapperspb.StringValue {
			return wrapperspb.String("decode failed")
		},
	})
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
	result, err := handler.Login(context.Background(), LoginRequest{
		Message: rpc.Message{
			Name:    "MceAuth",
			Payload: []byte{1, 2, 3},
			Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
		},
	})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if called {
		t.Fatal("handler should not be called on decode failure")
	}
	if !result.CloseAfterResponse || result.Identity.UID != 0 {
		t.Fatalf("result = %+v, want close failure", result)
	}
	var rsp wrapperspb.StringValue
	if result.Response == nil {
		t.Fatal("response = nil")
	}
	if err := result.Response.DecodePB(&rsp); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	if rsp.GetValue() != "decode failed" {
		t.Fatalf("response value = %q, want decode failed", rsp.GetValue())
	}
}

func TestTypedLoginHandlerClosesOnFailureByDefault(t *testing.T) {
	handler := NewTypedLoginHandler(TypedLoginOptions[*wrapperspb.StringValue, *wrapperspb.StringValue]{
		LoginTopic: "MceAuth",
		Handler: func(context.Context, LoginContext, *wrapperspb.StringValue) (Identity, *wrapperspb.StringValue) {
			return Identity{}, wrapperspb.String("failed")
		},
	})
	msg, err := rpc.NewMessagePB("MceAuth", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	result, err := handler.Login(context.Background(), LoginRequest{Message: msg})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if !result.CloseAfterResponse {
		t.Fatalf("CloseAfterResponse = false, want true")
	}
}

func TestTypedLoginHandlerKeepOpenOnFailure(t *testing.T) {
	handler := NewTypedLoginHandler(TypedLoginOptions[*wrapperspb.StringValue, *wrapperspb.StringValue]{
		LoginTopic:        "MceAuth",
		KeepOpenOnFailure: true,
		Handler: func(context.Context, LoginContext, *wrapperspb.StringValue) (Identity, *wrapperspb.StringValue) {
			return Identity{}, wrapperspb.String("failed")
		},
	})
	msg, err := rpc.NewMessagePB("MceAuth", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	result, err := handler.Login(context.Background(), LoginRequest{Message: msg})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if result.CloseAfterResponse {
		t.Fatalf("CloseAfterResponse = true, want false")
	}
}
