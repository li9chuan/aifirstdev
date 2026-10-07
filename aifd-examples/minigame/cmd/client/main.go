package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
	miniclient "github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/client"
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:18080/ws", "proxy websocket url")
	uid := flag.Int64("uid", 1001, "demo account uid")
	counterDelta := flag.Int("counter-delta", 1, "counter add delta after login")
	timeout := flag.Duration("timeout", 10*time.Second, "client operation timeout")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	accountApp, err := app.NewAccountApp()
	if err != nil {
		fail(err)
	}
	authReq, err := accountApp.DemoAuth(runCtx, *uid)
	if err != nil {
		fail(err)
	}
	client, err := miniclient.New(runCtx, miniclient.Options{
		URL:          *url,
		Timeout:      *timeout,
		AwaitTimeout: *timeout,
	})
	if err != nil {
		fail(err)
	}
	defer client.Close()

	authRsp, err := client.Login(runCtx, authReq)
	if err != nil {
		fail(err)
	}
	fmt.Printf("auth ret=%d uid=%d account=%d\n", authRsp.GetRet(), authReq.GetPayload().GetUid(), authReq.GetPayload().GetAccountId())

	counterRsp, err := client.AddCounter(runCtx, int32(*counterDelta))
	if err != nil {
		fail(err)
	}
	fmt.Printf("counter uid=%d value=%d limit=%d message=%s\n", counterRsp.GetUid(), counterRsp.GetValue(), counterRsp.GetLimit(), counterRsp.GetMessage())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
