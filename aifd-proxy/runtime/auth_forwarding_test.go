package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	proxylocal "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/local"
	"github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats/broker"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpclocal "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/local"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

type forwardingCaller struct {
	rpc.Session
	headers map[string]string
}

func (c forwardingCaller) Header(key string) string { return c.headers[key] }

// No account-center import or fabricated AIFD session is needed for this path.
func TestTrustedAuthContextAcrossLocalAndNATS(t *testing.T) {
	for _, kind := range []string{"local", "nats"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var sender, receiver rpc.Bus
			var b *broker.Broker
			if kind == "local" {
				sender = rpclocal.NewBus(rpclocal.Config{Buffer: 4})
				receiver = sender
			} else {
				var err error
				b, err = broker.Start(ctx, broker.Config{Options: &server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true}})
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close(context.Background())
				newBus := func(name string) rpc.Bus {
					bus, err := rpcnats.NewBus(rpcnats.Config{Buffer: 4, Options: natsgo.Options{Servers: []string{b.ClientURL()}, Name: name, Timeout: time.Second}})
					if err != nil {
						t.Fatal(err)
					}
					return bus
				}
				sender, receiver = newBus("gateway"), newBus("backend")
				defer receiver.Close(context.Background())
			}
			defer sender.Close(context.Background())
			identity := proxy.Identity{UID: 42, BackendID: "external", Account: "unrelated-uuid", ValidUntil: time.Now().Add(time.Minute).Truncate(time.Second)}
			seen := make(chan proxy.AuthContext, 1)
			if err := receiver.Register(rpc.Route{Name: "MceAccountInfo", Kind: rpc.RouteKindClient, Handler: func(ctx context.Context, s rpc.Session, msg rpc.Message) error {
				actual, err := proxy.GetAuthContext(forwardingCaller{Session: s, headers: msg.Header})
				if err != nil {
					return err
				}
				seen <- actual
				return s.Respond(ctx, rpc.Message{Name: "MseAccountInfo"})
			}}); err != nil {
				t.Fatal(err)
			}
			if natsBus, ok := receiver.(*rpcnats.Bus); ok {
				if err := natsBus.NatsConn().FlushTimeout(time.Second); err != nil {
					t.Fatal(err)
				}
			}
			runnerDone := make(chan struct{})
			go func() {
				defer close(runnerDone)
				for {
					select {
					case <-ctx.Done():
						return
					case d, ok := <-receiver.Deliveries():
						if !ok {
							return
						}
						_ = d.Handle(ctx)
					}
				}
			}()
			defer func() { cancel(); <-runnerDone }()
			delivery := proxyruntime.NewDelivery()
			conn := proxylocal.NewConnection("connection")
			if err := delivery.Add(conn); err != nil {
				t.Fatal(err)
			}
			gateway := proxyruntime.NewGateway(nil, proxyruntime.NewRPCUpstream(sender, delivery))
			session, err := gateway.AcceptIdentity(conn, identity)
			if err != nil {
				t.Fatal(err)
			}
			msg := rpc.Message{Name: "MceAccountInfo", Header: map[string]string{rpc.HeaderClientInbox: "7", proxy.HeaderAuthContext: "forged", strings.ToLower(proxy.HeaderAuthContext): "also-forged", proxy.HeaderCallerKind: proxy.CallerProxy, proxy.HeaderConnectionID: "forged", proxy.HeaderProxyInstanceID: "forged", proxy.HeaderSessionDeadline: "forged"}}
			if err = gateway.Receive(ctx, conn.ID(), msg); err != nil {
				t.Fatal(err)
			}
			select {
			case actual := <-seen:
				if actual != identity.AuthContext() {
					t.Fatalf("auth context changed: %+v", actual)
				}
			case <-ctx.Done():
				t.Fatal("backend context missing")
			}
			if len(conn.Sent()) != 1 || conn.Sent()[0].Name != "MseAccountInfo" {
				t.Fatal("reply did not reach original connection")
			}
			if session.ConnectionID != "connection" || msg.Header[proxy.HeaderAuthContext] != "forged" {
				t.Fatal("source message mutated")
			}
		})
	}
}

func TestGatewayRejectsMissingAndExpiredAuthenticationDeadline(t *testing.T) {
	upstream := proxylocal.NewUpstreamRecorder()
	g := proxyruntime.NewGateway(nil, upstream)
	conn := proxylocal.NewConnection("c")
	identity := proxy.Identity{UID: 42, BackendID: "external", Account: "uuid"}
	if _, err := g.AcceptIdentity(conn, identity); !errors.Is(err, proxy.ErrAuthContextInvalid) {
		t.Fatal(err)
	}
	identity.ValidUntil = time.Now().Add(30 * time.Millisecond)
	if _, err := g.AcceptIdentity(conn, identity); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(identity.ValidUntil) + time.Millisecond)
	if err := g.Receive(context.Background(), conn.ID(), rpc.Message{Name: "MceLate"}); !errors.Is(err, proxy.ErrAuthExpired) {
		t.Fatal(err)
	}
	if len(upstream.Envelopes()) != 0 {
		t.Fatal("expired request forwarded")
	}
}
