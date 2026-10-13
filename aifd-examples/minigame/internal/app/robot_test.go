package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	proxyws "github.com/li9chuan/aifirstdev/aifd-client/adapters/proxyws"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	authrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/robot"
	counterrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter/robot"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	robotc "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	robotrt "github.com/li9chuan/aifirstdev/aifd-robot/runtime"
)

func TestRobotLoginSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	appInstance, server := startRobotTestServer(t, ctx)
	defer appInstance.Close(context.Background())
	defer server.Close()

	modules := map[int64]*authrobot.AuthModule{}
	group := newRobotGroup(t, ctx, server.URL, 2, 1001, func(spec robotc.AgentSpec) *authrobot.AuthModule {
		module := authrobot.NewModule(authrobot.ModuleOptions{Auth: appInstance.Account.DemoAuth})
		modules[spec.UID] = module
		return module
	})
	defer group.Close(context.Background())

	if err := group.Start(ctx); err != nil {
		t.Fatalf("RunGroup Start error = %v", err)
	}
	for uid, module := range modules {
		rsp, err := module.Wait(ctx)
		if err != nil {
			t.Fatalf("robot uid %d Wait error = %v", uid, err)
		}
		if rsp.GetRet() != auth.RetOK {
			t.Fatalf("robot uid %d rsp = %+v, want auth ok", uid, &rsp)
		}
		owner, ok := appInstance.Game.Runtime.GetUser(uid)
		if !ok {
			t.Fatalf("owner %d is not active", uid)
		}
		if owner.Snapshot().GetAuth().GetUid() != uid {
			t.Fatalf("owner %d auth payload = %+v, want uid", uid, owner.Snapshot().GetAuth())
		}
	}
	report := group.Report()
	if report.Login != 2 || report.Send < 2 || report.Receive < 2 {
		t.Fatalf("report = %+v, want login/send/recv for 2 robots", report)
	}
}

func TestRobotCounterSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	appInstance, server := startRobotTestServer(t, ctx)
	defer appInstance.Close(context.Background())
	defer server.Close()

	authModules := map[int64]*authrobot.AuthModule{}
	counterModules := map[int64]*counterrobot.CounterModule{}
	group := newRobotCounterGroup(t, ctx, server.URL, 2, 1001, appInstance.Account.DemoAuth, authModules, counterModules)
	defer group.Close(context.Background())

	if err := group.Start(ctx); err != nil {
		t.Fatalf("RunGroup Start error = %v", err)
	}
	for uid, authModule := range authModules {
		rsp, err := authModule.Wait(ctx)
		if err != nil {
			t.Fatalf("robot uid %d auth Wait error = %v", uid, err)
		}
		if rsp.GetRet() != auth.RetOK {
			t.Fatalf("robot uid %d auth rsp = %+v, want auth ok", uid, &rsp)
		}
		counterRsp, err := counterModules[uid].Wait(ctx)
		if err != nil {
			t.Fatalf("robot uid %d counter Wait error = %v", uid, err)
		}
		if counterRsp.GetUid() != uid || counterRsp.GetValue() != 2 || counterRsp.GetLimit() != 10 {
			t.Fatalf("robot uid %d counter rsp = %+v, want value 2", uid, &counterRsp)
		}
	}
	if err := appInstance.DB.Flush(context.Background()); err != nil {
		t.Fatalf("DB Flush error = %v", err)
	}
	for uid := range authModules {
		owner, ok := appInstance.Game.Runtime.GetUser(uid)
		if !ok {
			t.Fatalf("owner %d is not active", uid)
		}
		if value := owner.Snapshot().GetCounter().GetValue(); value != 2 {
			t.Fatalf("owner %d counter = %d, want 2", uid, value)
		}
	}
	global := waitForWorldTotal(t, appInstance, 4)
	if global.GetTotalAdd() != 4 {
		t.Fatalf("global total add = %d, want 4", global.GetTotalAdd())
	}
	report := group.Report()
	if report.Login != 2 || report.Send < 4 || report.Receive < 4 {
		t.Fatalf("report = %+v, want auth and counter send/recv", report)
	}
}

func TestRobotLoginInvalidToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	appInstance, server := startRobotTestServer(t, ctx)
	defer appInstance.Close(context.Background())
	defer server.Close()

	var module *authrobot.AuthModule
	group := newRobotGroup(t, ctx, server.URL, 1, 1001, func(robotc.AgentSpec) *authrobot.AuthModule {
		module = authrobot.NewModule(authrobot.ModuleOptions{
			Auth: func(context.Context, int64) (*pbmsg.MceAuth, error) {
				return &pbmsg.MceAuth{
					Payload: &pbmsg.MAuthPayload{AccountId: 5001, Uid: 1001, RealmId: 1},
					Token:   "bad",
				}, nil
			},
		})
		return module
	})
	defer group.Close(context.Background())

	if err := group.Start(ctx); err != nil {
		t.Fatalf("RunGroup Start error = %v", err)
	}
	rsp, err := module.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait error = %v", err)
	}
	if rsp.GetRet() != auth.RetInvalidAuth {
		t.Fatalf("ret = %d, want invalid auth", rsp.GetRet())
	}
}

func startRobotTestServer(t *testing.T, ctx context.Context) (*AllInOneApp, *httptest.Server) {
	t.Helper()
	appInstance, err := NewAllInOneApp(ctx)
	if err != nil {
		t.Fatalf("NewAllInOneApp error = %v", err)
	}
	return appInstance, httptest.NewServer(appInstance.Proxy.Handler)
}

func newRobotGroup(
	t *testing.T,
	ctx context.Context,
	httpURL string,
	runNum int,
	uidStart int64,
	newModule func(robotc.AgentSpec) *authrobot.AuthModule,
) *robotrt.RunGroup {
	t.Helper()
	group, err := robotrt.NewRunGroup(robotrt.RunGroupOptions{
		Scenario: robotc.Scenario{
			Name:          "minigame-auth",
			RunNum:        runNum,
			UIDStart:      uidStart,
			AccountStart:  uidStart,
			AccountPrefix: "robot",
			TickInterval:  10 * time.Millisecond,
		},
		Transport: func(robotc.AgentSpec) (robotc.Transport, error) {
			return proxyws.NewWebSocketTransport(ctx, proxyws.WebSocketConfig{
				URL:         "ws" + strings.TrimPrefix(httpURL, "http"),
				DialTimeout: time.Second,
			})
		},
		Modules: func(spec robotc.AgentSpec) []robotc.AgentModule {
			module := newModule(spec)
			return []robotc.AgentModule{module}
		},
	})
	if err != nil {
		t.Fatalf("NewRunGroup error = %v", err)
	}
	return group
}

func newRobotCounterGroup(
	t *testing.T,
	ctx context.Context,
	httpURL string,
	runNum int,
	uidStart int64,
	authFn authrobot.AuthFunc,
	authModules map[int64]*authrobot.AuthModule,
	counterModules map[int64]*counterrobot.CounterModule,
) *robotrt.RunGroup {
	t.Helper()
	group, err := robotrt.NewRunGroup(robotrt.RunGroupOptions{
		Scenario: robotc.Scenario{
			Name:          "minigame-counter",
			RunNum:        runNum,
			UIDStart:      uidStart,
			AccountStart:  uidStart,
			AccountPrefix: "robot",
			TickInterval:  10 * time.Millisecond,
		},
		Transport: func(robotc.AgentSpec) (robotc.Transport, error) {
			return proxyws.NewWebSocketTransport(ctx, proxyws.WebSocketConfig{
				URL:         "ws" + strings.TrimPrefix(httpURL, "http"),
				DialTimeout: time.Second,
			})
		},
		Modules: func(spec robotc.AgentSpec) []robotc.AgentModule {
			authModule := authrobot.NewModule(authrobot.ModuleOptions{Auth: authFn})
			counterModule := counterrobot.NewModule(counterrobot.ModuleOptions{
				Auth:  authModule,
				Delta: 2,
			})
			authModules[spec.UID] = authModule
			counterModules[spec.UID] = counterModule
			return []robotc.AgentModule{authModule, counterModule}
		},
	})
	if err != nil {
		t.Fatalf("NewRunGroup error = %v", err)
	}
	return group
}
