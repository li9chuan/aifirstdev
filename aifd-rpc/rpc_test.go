package rpc

import (
	"context"
	"strconv"
	"testing"
	"time"

	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type notProto struct{}

func startTestBus(t *testing.T, reg *Registry) (context.Context, *LocalBus, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	bus := NewLocalBus(LocalConfig{ServiceName: "rpc-test", Buffer: 32, DefaultTimeout: time.Second})
	if err := Start(ctx, bus, reg, RunnerOptions{Workers: 2, QueueSize: 16}); err != nil {
		cancel()
		_ = bus.Close(context.Background())
		t.Fatalf("Start error = %v", err)
	}
	cleanup := func() {
		cancel()
		_ = bus.Close(context.Background())
	}
	return ctx, bus, cleanup
}

func callPB(t *testing.T, ctx context.Context, bus *LocalBus, uid int64, topic string, inbox int, req *wrapperspb.StringValue) contracts.Message {
	t.Helper()
	msg, err := contracts.NewMessagePB(topic, req)
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	if inbox > 0 {
		if msg.Header == nil {
			msg.Header = map[string]string{}
		}
		msg.Header[contracts.HeaderClientInbox] = strconv.Itoa(inbox)
	}
	rspCh := make(chan contracts.Message, 1)
	if err := bus.Call(ctx, uid, msg, func(_ context.Context, _ contracts.Session, rsp contracts.Message) error {
		rspCh <- rsp.Clone()
		return nil
	}, contracts.CallOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	select {
	case rsp := <-rspCh:
		return rsp
	case <-time.After(time.Second):
		t.Fatalf("Call response timeout")
		return contracts.Message{}
	}
}

func TestRegistryAcceptsOnlySessionPBHandlers(t *testing.T) {
	reg := NewRegistry()
	if err := Client(reg, "MceNoReq", func(*Session) {}); err != nil {
		t.Fatalf("Client no req error = %v", err)
	}
	if err := Client(reg, "MceWithReq", func(*Session, *wrapperspb.StringValue) {}); err != nil {
		t.Fatalf("Client with req error = %v", err)
	}

	tests := []struct {
		name    string
		handler any
	}{
		{name: "ContextParam", handler: func(context.Context, *Session) {}},
		{name: "UIDParam", handler: func(int64, *wrapperspb.StringValue) {}},
		{name: "ReturnError", handler: func(*Session, *wrapperspb.StringValue) error { return nil }},
		{name: "ReturnPB", handler: func(*Session, *wrapperspb.StringValue) *wrapperspb.StringValue { return nil }},
		{name: "NotProto", handler: func(*Session, *notProto) {}},
		{name: "ValueProto", handler: func(*Session, wrapperspb.StringValue) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Client(NewRegistry(), "Mce"+tt.name, tt.handler); err == nil {
				t.Fatalf("Client accepted unsupported handler")
			}
		})
	}
}

func TestSessionResponseUsesClientInboxAndAllowsOnce(t *testing.T) {
	reg := NewRegistry()
	results := make(chan Result, 2)
	if err := Client(reg, "MceInventoryInfo", func(s *Session, req *wrapperspb.StringValue) {
		results <- s.Response(wrapperspb.String(req.Value + ":rsp"))
		results <- s.Response(wrapperspb.String("again"))
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	rsp := callPB(t, ctx, bus, 7, "MceInventoryInfo", 123, wrapperspb.String("hello"))
	if rsp.Header[contracts.HeaderClientInbox] != "123" {
		t.Fatalf("client inbox = %q, want 123", rsp.Header[contracts.HeaderClientInbox])
	}
	if rsp.Header[contracts.HeaderResponse] != "StringValue" {
		t.Fatalf("rsp header = %q, want StringValue", rsp.Header[contracts.HeaderResponse])
	}
	if rsp.Header[contracts.HeaderPBType] != "StringValue" {
		t.Fatalf("pbType header = %q, want StringValue", rsp.Header[contracts.HeaderPBType])
	}
	var out wrapperspb.StringValue
	if err := rsp.DecodePB(&out); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	if out.Value != "hello:rsp" {
		t.Fatalf("rsp value = %q, want hello:rsp", out.Value)
	}
	first, second := <-results, <-results
	if !first.OK() {
		t.Fatalf("first result = %s, want ok", first.String())
	}
	if second.OK() || second.Code() != CodeDuplicateResponse {
		t.Fatalf("second result = %s, want duplicate failure", second.String())
	}
}

func TestSessionResponseAsSeparatesTopicAndPBType(t *testing.T) {
	reg := NewRegistry()
	if err := Client(reg, "MceNamedResponse", func(s *Session, req *wrapperspb.StringValue) {
		s.ResponseAs("MseInventoryInfo", wrapperspb.String(req.Value+":rsp"))
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	rsp := callPB(t, ctx, bus, 7, "MceNamedResponse", 123, wrapperspb.String("hello"))
	if rsp.Name != "MseInventoryInfo" {
		t.Fatalf("response name = %q, want MseInventoryInfo", rsp.Name)
	}
	if rsp.Header[contracts.HeaderResponse] != "MseInventoryInfo" {
		t.Fatalf("rsp header = %q, want MseInventoryInfo", rsp.Header[contracts.HeaderResponse])
	}
	if rsp.Header[contracts.HeaderPBType] != "StringValue" {
		t.Fatalf("pbType header = %q, want StringValue", rsp.Header[contracts.HeaderPBType])
	}
}

func TestHandlerWithoutResponseDoesNotAutoReply(t *testing.T) {
	reg := NewRegistry()
	called := make(chan struct{}, 1)
	if err := Client(reg, "MceNoResponse", func(s *Session) {
		called <- struct{}{}
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	if err := bus.TellPB(ctx, 1, "MceNoResponse", wrapperspb.String("noop")); err != nil {
		t.Fatalf("TellPB error = %v", err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("handler not called")
	}
	if pushes := bus.Pushes(); len(pushes) != 0 {
		t.Fatalf("pushes = %+v, want none", pushes)
	}
}

func TestSessionErrorAndPanicUseStatusHeader(t *testing.T) {
	reg := NewRegistry()
	if err := Client(reg, "MceError", func(s *Session) {
		s.Error("BAD_REQUEST", "bad request")
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	if err := Client(reg, "McePanic", func(s *Session) {
		panic("boom")
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	errorRsp := callPB(t, ctx, bus, 1, "MceError", 123, wrapperspb.String("x"))
	if errorRsp.Header[contracts.HeaderStatus] != "BAD_REQUEST" {
		t.Fatalf("error status = %q, want BAD_REQUEST", errorRsp.Header[contracts.HeaderStatus])
	}
	if errorRsp.Header[contracts.HeaderErrorMessage] != "bad request" {
		t.Fatalf("error message = %q, want bad request", errorRsp.Header[contracts.HeaderErrorMessage])
	}

	panicRsp := callPB(t, ctx, bus, 1, "McePanic", 123, wrapperspb.String("x"))
	if panicRsp.Header[contracts.HeaderStatus] != CodeInternal {
		t.Fatalf("panic status = %q, want %s", panicRsp.Header[contracts.HeaderStatus], CodeInternal)
	}
}

func TestSessionRequestAndForward(t *testing.T) {
	reg := NewRegistry()
	if err := Server(reg, "World.Echo", func(s *Session, req *wrapperspb.StringValue) {
		s.Response(wrapperspb.String(req.Value + ":world"))
	}); err != nil {
		t.Fatalf("Server error = %v", err)
	}
	if err := Client(reg, "MceRequest", func(s *Session, req *wrapperspb.StringValue) {
		var worldRsp wrapperspb.StringValue
		if result := s.Request("World.Echo", req, &worldRsp, WithTimeout(time.Second)); !result.OK() {
			s.Error(result.Code(), result.Message())
			return
		}
		s.Response(wrapperspb.String(worldRsp.Value + ":client"))
	}); err != nil {
		t.Fatalf("Client request error = %v", err)
	}
	if err := Client(reg, "MceForward", func(s *Session, req *wrapperspb.StringValue) {
		s.Forward("World.Echo", req, WithTimeout(time.Second))
	}); err != nil {
		t.Fatalf("Client forward error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	requestRsp := callPB(t, ctx, bus, 0, "MceRequest", 123, wrapperspb.String("hello"))
	var out wrapperspb.StringValue
	if err := requestRsp.DecodePB(&out); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	if out.Value != "hello:world:client" {
		t.Fatalf("request rsp = %q, want hello:world:client", out.Value)
	}
	if requestRsp.Header[contracts.HeaderClientInbox] != "123" {
		t.Fatalf("request client inbox = %q, want 123", requestRsp.Header[contracts.HeaderClientInbox])
	}
	if requestRsp.Header[contracts.HeaderResponse] != "StringValue" {
		t.Fatalf("request rsp header = %q, want StringValue", requestRsp.Header[contracts.HeaderResponse])
	}
	if requestRsp.Header[contracts.HeaderPBType] != "StringValue" {
		t.Fatalf("request pbType header = %q, want StringValue", requestRsp.Header[contracts.HeaderPBType])
	}

	forwardMsg, err := contracts.NewMessagePB("MceForward", wrapperspb.String("hello"))
	if err != nil {
		t.Fatalf("NewMessagePB forward error = %v", err)
	}
	forwardMsg.Header = map[string]string{contracts.HeaderClientInbox: "123"}
	if err := bus.Tell(ctx, 9, forwardMsg); err != nil {
		t.Fatalf("Tell forward error = %v", err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("forward push timeout")
		default:
			pushes := bus.Pushes()
			if len(pushes) == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if pushes[0].UID != 9 {
				t.Fatalf("forward push uid = %d, want 9", pushes[0].UID)
			}
			if pushes[0].Message.Header[contracts.HeaderClientInbox] != "123" {
				t.Fatalf("forward client inbox = %q, want 123", pushes[0].Message.Header[contracts.HeaderClientInbox])
			}
			if pushes[0].Message.Header[contracts.HeaderResponse] != "StringValue" {
				t.Fatalf("forward rsp header = %q, want StringValue", pushes[0].Message.Header[contracts.HeaderResponse])
			}
			if pushes[0].Message.Header[contracts.HeaderPBType] != "StringValue" {
				t.Fatalf("forward pbType header = %q, want StringValue", pushes[0].Message.Header[contracts.HeaderPBType])
			}
			return
		}
	}
}

func TestSessionTellDoesNotCarryClientInbox(t *testing.T) {
	reg := NewRegistry()
	seenInbox := make(chan int, 1)
	if err := Server(reg, "World.Notify", func(s *Session, req *wrapperspb.StringValue) {
		seenInbox <- s.ClientInbox()
	}); err != nil {
		t.Fatalf("Server error = %v", err)
	}
	if err := Client(reg, "MceTell", func(s *Session, req *wrapperspb.StringValue) {
		if result := s.Tell("World.Notify", req, WithTimeout(time.Second)); !result.OK() {
			s.Error(result.Code(), result.Message())
			return
		}
		s.Response(wrapperspb.String("done"))
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	_ = callPB(t, ctx, bus, 5, "MceTell", 456, wrapperspb.String("notify"))
	select {
	case inbox := <-seenInbox:
		if inbox != 0 {
			t.Fatalf("tell client inbox = %d, want 0", inbox)
		}
	case <-time.After(time.Second):
		t.Fatal("tell target not called")
	}
}

func TestStreamRouteUsesSessionPBShape(t *testing.T) {
	reg := NewRegistry()
	done := make(chan string, 1)
	if err := Stream(reg, "rewards.MailAdd", func(s *Session, req *wrapperspb.StringValue) {
		done <- req.Value
	}); err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	ctx, bus, cleanup := startTestBus(t, reg)
	defer cleanup()

	if err := bus.SavePBToStream(ctx, 1, "rewards.MailAdd", wrapperspb.String("mail")); err != nil {
		t.Fatalf("SavePBToStream error = %v", err)
	}
	select {
	case value := <-done:
		if value != "mail" {
			t.Fatalf("stream value = %q, want mail", value)
		}
	case <-time.After(time.Second):
		t.Fatal("stream handler not called")
	}
}
