package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
	"github.com/li9chuan/aifirstdev/aifd-observe/logging"
	"github.com/li9chuan/aifirstdev/aifd-runtime/entrypoint"
	runtimepprof "github.com/li9chuan/aifirstdev/aifd-runtime/pprof"
)

func main() {
	confPath := flag.String("conf", "", "shared service config path")
	addr := flag.String("addr", "127.0.0.1:18080", "websocket listen address")
	pprofAddr := flag.String("pprof", "", "optional pprof listen address")
	flag.Parse()

	ctx, stop := entrypoint.SignalContext(context.Background())
	defer stop()
	fileCfg, err := app.LoadServiceFileConfig(ctx, *confPath)
	if err != nil {
		panic(err)
	}
	if _, err := runtimepprof.Start(ctx, *pprofAddr); err != nil {
		panic(err)
	}
	logCfg := logging.Config{
		SourceService: "minigame",
		AlsoStdout:    true,
	}
	if *confPath != "" {
		logCfg, err = fileCfg.LoggingConfig("minigame")
		if err != nil {
			panic(err)
		}
	}
	closeLog, err := logging.InitDefault(logCfg)
	if err != nil {
		panic(err)
	}
	defer closeLog()

	appInstance, err := app.NewAllInOneApp(ctx, fileCfg)
	if err != nil {
		panic(err)
	}
	defer appInstance.Close(context.Background())

	server := app.NewWebSocketHTTPServer(*addr, appInstance.Proxy.Handler)

	fmt.Printf("minigame full-chain demo listening on ws://%s/ws\n", *addr)
	logging.InfoContext(ctx, "minigame listening", logging.String("addr", server.Addr))
	if err := app.ServeHTTP(ctx, server); err != nil {
		logging.ErrorContext(ctx, "minigame listen failed", logging.Err(err), logging.String("addr", server.Addr))
		panic(err)
	}
	logging.Info("minigame stopped", logging.String("addr", server.Addr))
}
