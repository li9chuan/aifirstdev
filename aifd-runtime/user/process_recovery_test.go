package user

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	client "github.com/li9chuan/aifirstdev/aifd-client"
	clientws "github.com/li9chuan/aifirstdev/aifd-client/adapters/proxyws"
	proxybroker "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats/broker"
	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type sessionProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	lines   <-chan string
	waited  bool
}

func (p *sessionProcess) kill() {
	if p.waited {
		return
	}
	_ = p.command.Process.Kill()
	_ = p.command.Wait()
	p.waited = true
}

func TestRealProxyKillAndBusinessProcessRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	broker, err := proxybroker.Start(ctx, proxybroker.Config{Options: &natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true}, ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(context.Background())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(role string) *sessionProcess {
		command := exec.CommandContext(ctx, executable, "-test.run=^TestSessionProcessHelper$", "-test.timeout=20s")
		command.Env = append(os.Environ(), "AIFD_TEST_SESSION_ROLE="+role, "AIFD_TEST_SESSION_NATS="+broker.ClientURL())
		output, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stderr = command.Stdout
		input, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		lines := make(chan string, 16)
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(output)
			for scanner.Scan() {
				lines <- scanner.Text()
			}
		}()
		process := &sessionProcess{command: command, input: input, lines: lines}
		t.Cleanup(process.kill)
		expectProcessLine(t, ctx, lines, "READY")
		return process
	}
	owner := start("owner")
	a, b := start("proxy"), start("proxy")
	bus, err := rpcnats.NewBus(rpcnats.Config{Options: natsgo.Options{Servers: []string{broker.ClientURL()}, Name: "process-audit", Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close(context.Background())
	count := func() int64 {
		msg, _ := contracts.NewMessagePB("ConnectionCount", wrapperspb.String("count"))
		rsp, err := checkedRequest(ctx, bus, 42, msg, contracts.RequestOptions{Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		value := new(wrapperspb.Int64Value)
		if err := rsp.DecodePB(value); err != nil {
			t.Fatal(err)
		}
		return value.Value
	}
	waitCount := func(want int64) {
		until := time.Now().Add(2 * time.Second)
		for time.Now().Before(until) {
			if count() == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("connection count=%d want=%d", count(), want)
	}
	waitCount(2)
	a.kill() // No Go defer or ClientSessionClosed can run in the killed process.
	waitCount(1)
	owner.kill()
	expectProcessLine(t, ctx, b.lines, "LOST")
	_ = start("owner")
	waitCount(0)
	if _, err := fmt.Fprintln(b.input, "PROBE"); err != nil {
		t.Fatal(err)
	}
	expectProcessLine(t, ctx, b.lines, "REJECTED") // Old connection cannot renew after restart.
	c := start("proxy")
	waitCount(1) // A freshly authenticated proxy creates a new valid binding.
	c.kill()
	waitCount(0)
}

func expectProcessLine(t *testing.T, ctx context.Context, lines <-chan string, want string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	var seen []string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("helper exited before %s: %v", want, seen)
			}
			if line == want {
				return
			}
			seen = append(seen, line)
		case <-ctx.Done():
			t.Fatalf("helper %s: %v (%v)", want, ctx.Err(), seen)
		case <-timer.C:
			t.Fatalf("helper %s timeout: %v", want, seen)
		}
	}
}

// The helper uses the same built test binary; it runs only when explicitly spawned
// by the bounded process test. Every child is terminated by its parent cleanup.
func TestSessionProcessHelper(t *testing.T) {
	role := os.Getenv("AIFD_TEST_SESSION_ROLE")
	if role == "" {
		return
	}
	ctx := context.Background()
	bus, err := rpcnats.NewBus(rpcnats.Config{Options: natsgo.Options{Servers: []string{os.Getenv("AIFD_TEST_SESSION_NATS")}, Name: "session-" + role, Timeout: time.Second}, DefaultTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if role == "owner" {
		reg := rpc.NewRegistry()
		rt := NewRuntime[*wrapperspb.Int64Value](reg, nil, nil, WithDefaultHeartbeat(), WithClientSessions(proxyruntime.ClientSessionOptions{TTL: 300 * time.Millisecond}))
		if err := rt.RegisterRPC(); err != nil {
			t.Fatal(err)
		}
		if err := rpc.Server(reg, "ConnectionCount", func(s *rpc.Session, _ *wrapperspb.StringValue) {
			s.Response(wrapperspb.Int64(int64(rt.OnlineConnections(s.UID()))))
		}); err != nil {
			t.Fatal(err)
		}
		if err := rpc.Start(ctx, bus, reg); err != nil {
			t.Fatal(err)
		}
		go func() { _ = rt.Run(ctx) }()
		if err := bus.NatsConn().Flush(); err != nil {
			t.Fatal(err)
		}
		fmt.Println("READY")
		select {}
	}
	delivery := proxyruntime.NewDelivery()
	upstream := proxyruntime.NewRPCUpstream(bus, delivery)
	upstream.Timeout = 200 * time.Millisecond
	gateway := proxyruntime.NewGateway(nil, upstream)
	ready, _ := contracts.NewMessagePB("MseReady", wrapperspb.String("ready"))
	handler := proxyws.NewServer(proxyws.Config{Bus: bus, Gateway: gateway, Delivery: delivery, DuplicatePolicy: proxyws.DuplicateAllow,
		HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
			return proxy.HandshakeResult{Handled: true, Identity: proxy.Identity{UID: 42}, InitialMessage: &ready}, nil
		}),
	})
	server := httptest.NewServer(handler)
	transport, err := clientws.NewWebSocketTransport(ctx, clientws.WebSocketConfig{URL: "ws" + server.URL[4:], DialTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Recv(ctx); err != nil {
		t.Fatal(err)
	}
	sdk, err := client.New(transport, client.Options{AwaitTimeout: 400 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.Start(); err != nil {
		t.Fatal(err)
	}
	probe := func(ctx context.Context) error {
		return sdk.Request(ctx, &proxypb.MceSessionHeartbeat{}, &proxypb.MseSessionHeartbeat{})
	}
	if err := probe(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sdk.StartHeartbeat(client.HeartbeatOptions{Interval: 50 * time.Millisecond, Timeout: 400 * time.Millisecond, MaxMisses: 1, Probe: probe, OnLost: func(error) { fmt.Println("LOST") }}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "PROBE" {
			err := probe(ctx)
			if err == nil || !strings.Contains(err.Error(), "SESSION_NOT_BOUND") {
				t.Fatalf("stale binding probe: %v", err)
			}
			fmt.Println("REJECTED")
		}
	}
}
