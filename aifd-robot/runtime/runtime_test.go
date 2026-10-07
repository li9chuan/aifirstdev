package runtime

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	"github.com/li9chuan/aifirstdev/aifd-robot/adapters/local"
	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestRunGroupCreatesAgentsAndReportsLogin(t *testing.T) {
	factory := local.NewTransportFactory()
	group, err := NewRunGroup(RunGroupOptions{
		Scenario:  robot.Scenario{Name: "test", RunNum: 2, UIDStart: 100, AccountStart: 50, AccountPrefix: "acc", TickInterval: time.Hour},
		Transport: factory.NewTransport,
	})
	if err != nil {
		t.Fatalf("NewRunGroup error = %v", err)
	}
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer group.Close(context.Background())

	if got := len(group.Agents()); got != 2 {
		t.Fatalf("agent count = %d, want 2", got)
	}
	if report := group.Report(); report.Login != 2 {
		t.Fatalf("login = %d, want 2", report.Login)
	}
}

func TestAgentTickOnlyAfterLogin(t *testing.T) {
	transport := local.NewTransport()
	tickCount := 0
	module := &testModule{tick: func(context.Context, robot.Agent) error {
		tickCount++
		return nil
	}}
	agent, err := NewAgent(AgentOptions{
		ID:           "agent-1",
		UID:          1,
		Account:      "acc1",
		Transport:    transport,
		Modules:      []robot.AgentModule{module},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := agent.Attach(ctx); err != nil {
		t.Fatalf("Attach error = %v", err)
	}
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if tickCount != 0 {
		t.Fatalf("tick before login = %d, want 0", tickCount)
	}
	if err := agent.Login(ctx); err != nil {
		t.Fatalf("Login error = %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if tickCount == 0 {
		t.Fatal("tick after login = 0, want > 0")
	}
	if err := agent.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAgentOnRoutesTypedPush(t *testing.T) {
	transport := local.NewTransport()
	handled := make(chan string, 1)
	module := &testModule{attach: func(ctx context.Context, agent robot.Agent) error {
		return agent.On(func(msg *wrapperspb.StringValue) {
			handled <- msg.GetValue()
		})
	}}
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport, Modules: []robot.AgentModule{module}})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := agent.Attach(ctx); err != nil {
		t.Fatalf("Attach error = %v", err)
	}
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	transport.Inject(newStringMessage(t, "StringValue", "push"))
	select {
	case value := <-handled:
		if value != "push" {
			t.Fatalf("push value = %q, want push", value)
		}
	case <-time.After(time.Second):
		t.Fatal("typed push handler was not called")
	}
	if err := agent.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAgentCallMatchesClientInbox(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = agent.Close(context.Background()) }()

	done := make(chan requestResult, 1)
	if err := agent.Call(wrapperspb.String("req"), func(rsp *wrapperspb.StringValue, err error) {
		if err != nil {
			done <- requestResult{err: err}
			return
		}
		done <- requestResult{value: rsp.GetValue()}
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}

	sent := waitForSent(t, transport, 1)
	if sent[0].Name != "StringValue" {
		t.Fatalf("message name = %q, want protobuf type name", sent[0].Name)
	}
	inbox := sent[0].Header[rpc.HeaderClientInbox]
	if value, err := strconv.Atoi(inbox); err != nil || value <= 0 {
		t.Fatalf("client inbox = %q, want positive integer", inbox)
	}
	rsp := newStringMessage(t, "MseEcho", "rsp")
	rsp.Header[rpc.HeaderClientInbox] = inbox
	transport.Inject(rsp)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Call callback error = %v", result.err)
		}
		if result.value != "rsp" {
			t.Fatalf("response = %q, want rsp", result.value)
		}
	case <-time.After(time.Second):
		t.Fatal("Call callback did not complete")
	}
}

func TestAgentCallWithoutCallbackDoesNotAllocateClientInbox(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	if err := agent.Call(wrapperspb.String("req")); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	sent := waitForSent(t, transport, 1)
	if sent[0].Name != "StringValue" {
		t.Fatalf("message name = %q, want protobuf type name", sent[0].Name)
	}
	if got := sent[0].Header[rpc.HeaderClientInbox]; got != "" {
		t.Fatalf("client inbox = %q, want empty", got)
	}
}

func TestAgentCallInvalidHandlerDoesNotSend(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	err = agent.Call(wrapperspb.String("req"), func(*wrapperspb.StringValue) {})
	if err == nil {
		t.Fatal("Call error = nil, want invalid handler error")
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestAgentCallWithCallbackBeforeStartDoesNotSend(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}

	err = agent.Call(wrapperspb.String("req"), func(*wrapperspb.StringValue, error) {})
	if !errors.Is(err, ErrAgentNotStarted) {
		t.Fatalf("Call error = %v, want ErrAgentNotStarted", err)
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestAgentCallMatchesConcurrentRepliesByInbox(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = agent.Close(context.Background()) }()

	results := make(chan requestResult, 2)
	for _, value := range []string{"one", "two"} {
		value := value
		if err := agent.Call(wrapperspb.String(value), func(rsp *wrapperspb.StringValue, err error) {
			result := requestResult{name: value, err: err}
			if rsp != nil {
				result.value = rsp.GetValue()
			}
			results <- result
		}); err != nil {
			t.Fatalf("Call %s error = %v", value, err)
		}
	}

	sent := waitForSent(t, transport, 2)
	inboxes := map[string]string{}
	for _, msg := range sent {
		var req wrapperspb.StringValue
		if err := msg.DecodePB(&req); err != nil {
			t.Fatalf("request decode error = %v", err)
		}
		inboxes[req.GetValue()] = msg.Header[rpc.HeaderClientInbox]
	}
	if inboxes["one"] == "" || inboxes["two"] == "" {
		t.Fatalf("missing inboxes: %+v", inboxes)
	}
	if inboxes["one"] == inboxes["two"] {
		t.Fatalf("duplicate inbox %q", inboxes["one"])
	}

	for _, value := range []string{"two", "one"} {
		rsp := newStringMessage(t, "MseEcho", "rsp-"+value)
		rsp.Header[rpc.HeaderClientInbox] = inboxes[value]
		transport.Inject(rsp)
	}

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("Call %s error = %v", result.name, result.err)
			}
			got[result.name] = result.value
		case <-time.After(time.Second):
			t.Fatal("concurrent Call callbacks did not complete")
		}
	}
	if got["one"] != "rsp-one" || got["two"] != "rsp-two" {
		t.Fatalf("responses = %+v, want per-inbox replies", got)
	}
}

func TestAgentCallTimeoutRemovesCallback(t *testing.T) {
	transport := local.NewTransport()
	recorder := memory.NewRecorder()
	agent, err := NewAgent(AgentOptions{
		ID:              "agent-1",
		UID:             1,
		Account:         "acc1",
		Transport:       transport,
		Observe:         recorder,
		CallbackTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	routeHit := make(chan struct{}, 1)
	if err := agent.On(func(*wrapperspb.StringValue) {
		routeHit <- struct{}{}
	}); err != nil {
		t.Fatalf("On error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = agent.Close(context.Background()) }()

	done := make(chan error, 1)
	if err := agent.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}

	sent := waitForSent(t, transport, 1)
	inbox := sent[0].Header[rpc.HeaderClientInbox]
	select {
	case err := <-done:
		if !errors.Is(err, ErrAgentAwaitTimeout) {
			t.Fatalf("Call callback error = %v, want ErrAgentAwaitTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call timeout did not complete")
	}

	rsp := newStringMessage(t, "MseTimeout", "late")
	rsp.Header[rpc.HeaderClientInbox] = inbox
	transport.Inject(rsp)
	select {
	case <-routeHit:
		t.Fatal("late response was routed after request timeout")
	case <-time.After(20 * time.Millisecond):
	}
	if got := agent.client.Stats().Error; got != 1 {
		t.Fatalf("report error = %d, want 1 stale reply", got)
	}
	if !hasMetric(recorder.Events(), "client_stale_reply", inbox) {
		t.Fatalf("observe events = %+v, want client_stale_reply for inbox %s", recorder.Events(), inbox)
	}
}

func TestAgentCallReturnsWhenAgentCloses(t *testing.T) {
	transport := local.NewTransport()
	agent, err := NewAgent(AgentOptions{ID: "agent-1", UID: 1, Account: "acc1", Transport: transport, CallbackTimeout: time.Hour})
	if err != nil {
		t.Fatalf("NewAgent error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	done := make(chan error, 1)
	if err := agent.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	waitForSent(t, transport, 1)
	if err := agent.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrAgentClosed) {
			t.Fatalf("Call callback error = %v, want ErrAgentClosed", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Call did not return after agent close")
	}
}

func TestBehaviorAndObserve(t *testing.T) {
	recorder := memory.NewRecorder()
	factory := local.NewTransportFactory()
	group, err := NewRunGroup(RunGroupOptions{
		Scenario: robot.Scenario{
			Name:         "behavior",
			RunNum:       1,
			TickInterval: 10 * time.Millisecond,
			Behavior:     robot.BehaviorSpec{Name: "send_ping"},
		},
		Transport: factory.NewTransport,
		Modules: func(robot.AgentSpec) []robot.AgentModule {
			return []robot.AgentModule{&testModule{actions: []robot.ActionRegistration{{
				Name: "send_ping",
				Factory: func(robot.Settings) robot.Action {
					return robot.ActionFunc(func(ctx context.Context, agent robot.Agent) robot.Status {
						_ = agent.Call(wrapperspb.String("ping"))
						return robot.StatusSuccess
					})
				},
			}}}}
		},
		Observe: recorder,
	})
	if err != nil {
		t.Fatalf("NewRunGroup error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := group.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if report := group.Report(); report.Send == 0 {
		t.Fatal("send count = 0, want > 0")
	}
	if len(recorder.Events()) == 0 {
		t.Fatal("observe recorder has no events")
	}
	if err := group.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

type testModule struct {
	attach  func(context.Context, robot.Agent) error
	tick    func(context.Context, robot.Agent) error
	actions []robot.ActionRegistration
}

func (m *testModule) Name() string { return "test" }

func (m *testModule) OnAttach(ctx context.Context, agent robot.Agent) error {
	if m.attach != nil {
		return m.attach(ctx, agent)
	}
	return nil
}

func (m *testModule) OnLogin(context.Context, robot.Agent) error { return nil }

func (m *testModule) Tick(ctx context.Context, agent robot.Agent) error {
	if m.tick != nil {
		return m.tick(ctx, agent)
	}
	return nil
}

func (m *testModule) Actions() []robot.ActionRegistration { return m.actions }

type requestResult struct {
	name  string
	value string
	err   error
}

func waitForSent(t *testing.T, transport *local.Transport, count int) []rpc.Message {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sent := transport.Sent()
		if len(sent) >= count {
			return sent
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sent message count < %d", count)
	return nil
}

func newStringMessage(t *testing.T, name string, value string) rpc.Message {
	t.Helper()
	msg, err := rpc.NewMessagePB(name, wrapperspb.String(value))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	return msg
}

func hasMetric(events []observe.Event, name string, inbox string) bool {
	for _, event := range events {
		if event.Name == name && event.Dimensions["inbox"] == inbox {
			return true
		}
	}
	return false
}
