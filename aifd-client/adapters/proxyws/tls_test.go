package proxyws

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

func TestWSSPreservesProxyPackets(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		head, body, err := proxycodec.DecodePacket(data)
		if err != nil || head.GetTopic() != "McePing" || head.GetInbox() != 17 || string(body) != "payload" {
			t.Errorf("packet changed over WSS: %+v %v", head, err)
			return
		}
		_ = conn.Write(r.Context(), kind, data)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	transport, err := NewWebSocketTransport(ctx, WebSocketConfig{
		URL: "wss" + strings.TrimPrefix(server.URL, "https"), RequireTLS: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close(context.Background())
	msg := rpc.Message{Name: "McePing", Header: map[string]string{"s": "17", rpc.HeaderPBType: "McePing"}, Payload: []byte("payload")}
	if err := transport.Send(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got, err := transport.Recv(ctx)
	if err != nil || got.Name != msg.Name || got.Header["s"] != "17" || string(got.Payload) != "payload" {
		t.Fatalf("WSS packet roundtrip %+v: %v", got, err)
	}
}

func TestTLSRequirementRejectsPlaintextBeforeDial(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	_, err := NewWebSocketTransport(context.Background(), WebSocketConfig{URL: server.URL, RequireTLS: true})
	if !errors.Is(err, ErrInsecureTransport) || calls.Load() != 0 {
		t.Fatalf("plaintext policy: %v, requests=%d", err, calls.Load())
	}
}

func TestSecureRedirectsRejectDowngradeAndCrossOrigin(t *testing.T) {
	for _, secureTarget := range []bool{false, true} {
		t.Run(map[bool]string{false: "downgrade", true: "cross origin"}[secureTarget], func(t *testing.T) {
			var reached atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) })
			var target *httptest.Server
			if secureTarget {
				target = httptest.NewTLSServer(handler)
			} else {
				target = httptest.NewServer(handler)
			}
			defer target.Close()
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			client := source.Client()
			_, err := NewWebSocketTransport(context.Background(), WebSocketConfig{
				URL: source.URL, HTTPClient: client, Header: http.Header{"Authorization": {"Bearer synthetic-test"}},
			})
			if !errors.Is(err, ErrInsecureTransport) || reached.Load() != 0 {
				t.Fatalf("redirect policy: %v, target requests=%d", err, reached.Load())
			}
		})
	}
}

func TestCallerRedirectHookCannotBypassPolicyAndIsNotMutated(t *testing.T) {
	var leaked atomic.Int64
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
	}))
	defer secure.Close()
	client := secure.Client()
	var hooks atomic.Int64
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		hooks.Add(1)
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(plain.URL, "http://")
		return nil
	}
	_, err := NewWebSocketTransport(context.Background(), WebSocketConfig{URL: secure.URL, HTTPClient: client, RequireTLS: true})
	if !errors.Is(err, ErrInsecureTransport) || leaked.Load() != 0 || hooks.Load() != 1 {
		t.Fatalf("caller hook bypass: %v, hooks=%d, leaked=%d", err, hooks.Load(), leaked.Load())
	}
	// The original client's hook remains available; the SDK adds policy on a copy.
	req, _ := http.NewRequest(http.MethodGet, secure.URL, nil)
	if err := client.CheckRedirect(req, nil); err != nil || req.URL.Scheme != "http" {
		t.Fatalf("caller client was mutated: %v, URL=%s", err, req.URL)
	}
}

func TestWSSRejectsDisabledCertificateVerification(t *testing.T) {
	_, err := websocketHTTPClient(WebSocketConfig{URL: "wss://example.test/ws", HTTPClient: &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}})
	if !errors.Is(err, ErrInsecureTransport) {
		t.Fatalf("disabled certificate verification accepted: %v", err)
	}
}

func TestWSSWithHTTP2CapableServer(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err == nil {
			defer conn.CloseNow()
			_, _, _ = conn.Read(r.Context())
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport, err := NewWebSocketTransport(ctx, WebSocketConfig{URL: server.URL, HTTPClient: client, RequireTLS: true})
	if err != nil {
		t.Fatalf("WSS must remain usable when HTTPS advertises HTTP/2: %v", err)
	}
	_ = transport.Close(ctx)
}

func TestRedirectHookCannotChangeHTTPAuthority(t *testing.T) {
	var reached atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
			return
		}
		reached.Add(1)
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		req.Host = "other.example.test"
		return nil
	}
	_, err := NewWebSocketTransport(context.Background(), WebSocketConfig{URL: server.URL, HTTPClient: client, RequireTLS: true})
	if !errors.Is(err, ErrInsecureTransport) || reached.Load() != 0 {
		t.Fatalf("redirect changed HTTP authority: %v, target requests=%d", err, reached.Load())
	}
}
