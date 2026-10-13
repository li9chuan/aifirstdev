package proxyws

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	client "github.com/li9chuan/aifirstdev/aifd-client"
	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestClientSessionErrorOverWebSocket(t *testing.T) {
	bus := rpc.NewLocalBus(rpc.LocalConfig{ServiceName: "ws-error-test", Buffer: 8})
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	reg := rpc.NewRegistry()
	if err := rpc.Server(reg, "MceReject", func(s *rpc.Session, _ *wrapperspb.StringValue) {
		s.Error(rpc.CodeInvalid, "请求无效")
	}); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Server(reg, "MceEcho", func(s *rpc.Session, req *wrapperspb.StringValue) {
		s.ResponseAs("MseEcho", req)
	}); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Start(context.Background(), bus, reg, rpc.RunnerOptions{Workers: 1, QueueSize: 8}); err != nil {
		t.Fatal(err)
	}
	server := newRPCReplyServer(t, func(ctx context.Context, msg contracts.Message) ([][]byte, error) {
		rsp, err := bus.Request(ctx, 1, msg, rpc.RequestOptions{Timeout: time.Second})
		if err != nil {
			return nil, err
		}
		return proxycodec.EncodeMessage(rsp)
	})
	c := newWebSocketClient(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rsp := wrapperspb.String("unchanged")
	err := c.RequestAs(ctx, "MceReject", wrapperspb.String("request"), rsp)
	assertStatusError(t, err, rpc.CodeInvalid, "请求无效")
	if rsp.Value != "unchanged" {
		t.Fatalf("failed RequestAs decoded a business response: %+v", rsp)
	}

	callbacks := make(chan wsCallbackResult, 2)
	if err := c.CallAs("MceReject", wrapperspb.String("request"), func(rsp *wrapperspb.StringValue, err error) {
		callbacks <- wsCallbackResult{rsp, err}
	}); err != nil {
		t.Fatalf("CallAs send error = %v", err)
	}
	select {
	case result := <-callbacks:
		assertStatusError(t, result.err, rpc.CodeInvalid, "请求无效")
		if result.rsp != nil {
			t.Fatalf("failed CallAs response = %+v, want nil", result.rsp)
		}
	case <-ctx.Done():
		t.Fatal("CallAs callback did not finish")
	}

	// A later reply is a barrier for the same connection's read loop.
	if err := c.RequestAs(ctx, "MceEcho", wrapperspb.String("still usable"), rsp); err != nil || rsp.Value != "still usable" {
		t.Fatalf("request after failure = %+v, %v", rsp, err)
	}
	if err := c.RequestAs(ctx, "MceEcho", &wrapperspb.StringValue{}, rsp); err != nil || rsp.Value != "" {
		t.Fatalf("empty protobuf success = %+v, %v", rsp, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-callbacks:
		t.Fatalf("CallAs callback ran more than once: %+v", result)
	default:
	}
}

func TestClientLegacyRPCErrorOverWebSocket(t *testing.T) {
	server := newRPCReplyServer(t, func(_ context.Context, msg contracts.Message) ([][]byte, error) {
		head, err := proxycodec.HeadFromMessage(msg)
		if err != nil {
			return nil, err
		}
		return proxycodec.EncodePacket(&proxypb.MsgHead{Inbox: head.Inbox, Topic: "RPCError", PbType: "RPCError"}, nil)
	})
	c := newWebSocketClient(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rsp wrapperspb.StringValue
	assertStatusError(t, c.RequestAs(ctx, "MceReject", &wrapperspb.StringValue{}, &rsp), rpc.CodeInternal, "rpc request failed")
	callbacks := make(chan wsCallbackResult, 1)
	if err := c.CallAs("MceReject", &wrapperspb.StringValue{}, func(rsp *wrapperspb.StringValue, err error) {
		callbacks <- wsCallbackResult{rsp, err}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-callbacks:
		assertStatusError(t, result.err, rpc.CodeInternal, "rpc request failed")
		if result.rsp != nil {
			t.Fatalf("legacy error response = %+v, want nil", result.rsp)
		}
	case <-ctx.Done():
		t.Fatal("legacy error callback did not finish")
	}
}

func TestClientMalformedRPCErrorEndsAwaitOverWebSocket(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "RequestAs", true: "CallAs"}[async], func(t *testing.T) {
			server := newRPCReplyServer(t, func(_ context.Context, msg contracts.Message) ([][]byte, error) {
				head, err := proxycodec.HeadFromMessage(msg)
				if err != nil {
					return nil, err
				}
				return proxycodec.EncodePacket(&proxypb.MsgHead{Inbox: head.Inbox, Topic: "RPCError", PbType: "RPCError"}, []byte{0xff})
			})
			c := newWebSocketClient(t, server)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var err error
			if async {
				callbacks := make(chan wsCallbackResult, 1)
				if err := c.CallAs("MceReject", &wrapperspb.StringValue{}, func(rsp *wrapperspb.StringValue, err error) {
					callbacks <- wsCallbackResult{rsp, err}
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case result := <-callbacks:
					err = result.err
					if result.rsp != nil {
						t.Fatalf("malformed error response = %+v, want nil", result.rsp)
					}
				case <-ctx.Done():
					t.Fatal("malformed error callback did not finish")
				}
			} else {
				err = c.RequestAs(ctx, "MceReject", &wrapperspb.StringValue{}, &wrapperspb.StringValue{})
			}
			if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, client.ErrAwaitTimeout) {
				t.Fatalf("malformed payload error = %v, want immediate decode failure", err)
			}
			select {
			case <-c.Done():
			case <-ctx.Done():
				t.Fatal("malformed error did not terminate the transport")
			}
		})
	}
}

type wsCallbackResult struct {
	rsp *wrapperspb.StringValue
	err error
}

func assertStatusError(t *testing.T, err error, code, message string) {
	t.Helper()
	var status *rpc.StatusError
	if !errors.As(err, &status) || status.Code != code || status.Message != message {
		t.Fatalf("error = %v, want StatusError %s / %s", err, code, message)
	}
}

func newWebSocketClient(t *testing.T, server *httptest.Server) *client.Client {
	t.Helper()
	c, err := client.New(newTestWebSocketTransport(t, server), client.Options{AwaitTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

// newRPCReplyServer uses the production WS codec with a real request/reply socket.
func newRPCReplyServer(t *testing.T, reply func(context.Context, contracts.Message) ([][]byte, error)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			_, data, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			head, body, err := proxycodec.DecodePacket(data)
			if err != nil {
				t.Errorf("server DecodePacket: %v", err)
				return
			}
			msg, err := proxycodec.MessageFromPacket(head, body)
			if err != nil {
				t.Errorf("server MessageFromPacket: %v", err)
				return
			}
			parts, err := reply(r.Context(), msg)
			if err != nil {
				t.Errorf("server reply: %v", err)
				return
			}
			if err := ws.Write(r.Context(), coderws.MessageBinary, bytes.Join(parts, nil)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}
