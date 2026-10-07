package proxyws

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	coderws "github.com/coder/websocket"
)

func TestWebSocketTransportSendEncodesProxyPacket(t *testing.T) {
	got := make(chan *proxypb.MsgHead, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(coderws.StatusNormalClosure, "done")
		_, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		head, body, err := proxycodec.DecodePacket(data)
		if err != nil || !bytes.Equal(body, []byte{1, 2}) {
			return
		}
		got <- head
	}))
	defer server.Close()

	transport := newTestWebSocketTransport(t, server)
	defer transport.Close(context.Background())
	if err := transport.Send(context.Background(), rpc.Message{
		Name:    "MceAuth",
		Payload: []byte{1, 2},
		Header:  map[string]string{rpc.HeaderPBType: "MceAuth"},
	}); err != nil {
		t.Fatalf("Send error = %v", err)
	}
	select {
	case head := <-got:
		if head.Topic != "MceAuth" || head.PbType != "MceAuth" {
			t.Fatalf("head = %+v, want MceAuth", head)
		}
	case <-time.After(time.Second):
		t.Fatal("server read timeout")
	}
}

func TestWebSocketTransportRecvDecodesProxyPacket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(coderws.StatusNormalClosure, "done")
		parts, err := proxycodec.EncodePacket(&proxypb.MsgHead{Topic: "MseAuth", PbType: "MseAuth"}, []byte{3, 4})
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), coderws.MessageBinary, bytes.Join(parts, nil))
	}))
	defer server.Close()

	transport := newTestWebSocketTransport(t, server)
	defer transport.Close(context.Background())
	msg, err := transport.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv error = %v", err)
	}
	if msg.Name != "MseAuth" || msg.Header[rpc.HeaderPBType] != "MseAuth" || !bytes.Equal(msg.Payload, []byte{3, 4}) {
		t.Fatalf("message = %+v, want MseAuth", msg)
	}
}

func TestWebSocketTransportRecvRejectsTextFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(coderws.StatusNormalClosure, "done")
		_ = ws.Write(r.Context(), coderws.MessageText, []byte("bad"))
	}))
	defer server.Close()

	transport := newTestWebSocketTransport(t, server)
	defer transport.Close(context.Background())
	if _, err := transport.Recv(context.Background()); !errors.Is(err, ErrUnexpectedMessageType) {
		t.Fatalf("Recv error = %v, want ErrUnexpectedMessageType", err)
	}
}

func TestWebSocketTransportRecvRejectsInvalidPacket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(coderws.StatusNormalClosure, "done")
		_ = ws.Write(r.Context(), coderws.MessageBinary, []byte{1, 2, 3})
	}))
	defer server.Close()

	transport := newTestWebSocketTransport(t, server)
	defer transport.Close(context.Background())
	if _, err := transport.Recv(context.Background()); err == nil {
		t.Fatal("Recv error = nil, want invalid packet error")
	}
}

func TestWebSocketTransportCloseIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(coderws.StatusNormalClosure, "done")
		<-r.Context().Done()
	}))
	defer server.Close()

	transport := newTestWebSocketTransport(t, server)
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
}

func newTestWebSocketTransport(t *testing.T, server *httptest.Server) *WebSocketTransport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	transport, err := NewWebSocketTransport(ctx, WebSocketConfig{
		URL:         "ws" + strings.TrimPrefix(server.URL, "http"),
		DialTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewWebSocketTransport error = %v", err)
	}
	return transport
}
