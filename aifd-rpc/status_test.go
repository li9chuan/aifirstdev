package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestRequestPBReturnsStatusErrorForHandlerPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewLocalBus(LocalConfig{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	reg := NewRegistry()
	if err := Server(reg, "panic", func(*Session, *wrapperspb.StringValue) {
		panic("boom")
	}); err != nil {
		t.Fatalf("Server error = %v", err)
	}
	if err := Start(ctx, bus, reg, RunnerOptions{Workers: 1, QueueSize: 8}); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	var rsp wrapperspb.StringValue
	err := bus.RequestPB(ctx, 1, "panic", wrapperspb.String("req"), &rsp, RequestOptions{Timeout: time.Second})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("RequestPB error = %v, want StatusError", err)
	}
	if statusErr.Code != CodeInternal || statusErr.Message != "rpc handler panic" {
		t.Fatalf("status error = %+v, want %s rpc handler panic", statusErr, CodeInternal)
	}
}

func TestRequestReturnsStatusErrorForDecodeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewLocalBus(LocalConfig{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	reg := NewRegistry()
	if err := Server(reg, "decode", func(s *Session, _ *wrapperspb.StringValue) {
		s.Response(wrapperspb.String("unreachable"))
	}); err != nil {
		t.Fatalf("Server error = %v", err)
	}
	if err := Start(ctx, bus, reg, RunnerOptions{Workers: 1, QueueSize: 8}); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	response, err := bus.Request(ctx, 1, Message{Name: "decode", Payload: []byte{0xff}}, RequestOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	err = StatusFromMessage(response)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("StatusFromMessage error = %v, want StatusError", err)
	}
	if statusErr.Code != CodeDecode {
		t.Fatalf("status code = %s, want %s", statusErr.Code, CodeDecode)
	}
}
