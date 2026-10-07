package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	proxyws "github.com/li9chuan/aifirstdev/aifd-client/adapters/proxyws"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	authrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/robot"
	counterrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter/robot"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
	robotc "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	robotrt "github.com/li9chuan/aifirstdev/aifd-robot/runtime"
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:18080/ws", "proxy websocket url")
	runNum := flag.Int("run-num", 1, "robot agent count")
	uidStart := flag.Int64("uid-start", 1001, "first robot uid")
	counterDelta := flag.Int("counter-delta", 1, "counter add delta after login")
	timeout := flag.Duration("timeout", 10*time.Second, "login wait timeout")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	waitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	accountApp, err := app.NewAccountApp()
	if err != nil {
		fail(err)
	}

	var modules []*authrobot.AuthModule
	var counters []*counterrobot.CounterModule
	group, err := robotrt.NewRunGroup(robotrt.RunGroupOptions{
		Scenario: robotc.Scenario{
			Name:          "minigame-auth",
			RunNum:        *runNum,
			UIDStart:      *uidStart,
			AccountStart:  *uidStart,
			AccountPrefix: "robot",
			TickInterval:  time.Second,
		},
		Transport: func(robotc.AgentSpec) (robotc.Transport, error) {
			return proxyws.NewWebSocketTransport(waitCtx, proxyws.WebSocketConfig{
				URL:         *url,
				DialTimeout: *timeout,
			})
		},
		Modules: func(robotc.AgentSpec) []robotc.AgentModule {
			authModule := authrobot.NewModule(authrobot.ModuleOptions{Auth: accountApp.DemoAuth})
			counterModule := counterrobot.NewModule(counterrobot.ModuleOptions{
				Auth:  authModule,
				Delta: int32(*counterDelta),
			})
			modules = append(modules, authModule)
			counters = append(counters, counterModule)
			return []robotc.AgentModule{authModule, counterModule}
		},
	})
	if err != nil {
		fail(err)
	}
	defer group.Close(context.Background())

	if err := group.Start(waitCtx); err != nil {
		fail(err)
	}
	for i, module := range modules {
		rsp, err := module.Wait(waitCtx)
		if err != nil {
			fail(fmt.Errorf("robot %d wait auth: %w", i, err))
		}
		if rsp.GetRet() != auth.RetOK {
			fail(fmt.Errorf("robot %d auth failed: ret=%d", i, rsp.GetRet()))
		}
		fmt.Printf("robot %d auth ok\n", i)
	}
	for i, module := range counters {
		rsp, err := module.Wait(waitCtx)
		if err != nil {
			fail(fmt.Errorf("robot %d wait counter: %w", i, err))
		}
		fmt.Printf("robot %d counter uid=%d value=%d limit=%d message=%s\n", i, rsp.GetUid(), rsp.GetValue(), rsp.GetLimit(), rsp.GetMessage())
	}

	report := group.Report()
	fmt.Printf("robot summary login=%d send=%d recv=%d error=%d\n", report.Login, report.Send, report.Receive, report.Error)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
