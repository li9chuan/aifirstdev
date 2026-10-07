package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
	proxybroker "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats/broker"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
	"github.com/li9chuan/aifirstdev/aifd-runtime/entrypoint"
	runtimepprof "github.com/li9chuan/aifirstdev/aifd-runtime/pprof"
)

func main() {
	confPath := flag.String("conf", "", "shared service config path")
	addr := flag.String("addr", "127.0.0.1:18080", "proxy websocket listen address")
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

	var closeComponents []entrypoint.Component
	defer func() {
		_ = entrypoint.Close(context.Background(), closeComponents...)
	}()

	var embeddedBroker *proxybroker.Broker
	if fileCfg.ProxyBroker.Enabled {
		var err error
		embeddedBroker, err = proxybroker.Start(ctx, proxyBrokerConfig(fileCfg.ProxyBroker))
		if err != nil {
			panic(err)
		}
		closeComponents = append(closeComponents, entrypoint.Component{Name: "proxy-broker", Close: embeddedBroker.Close})
	}

	natsCfg, err := fileCfg.NATSConfig("minigame-proxy", "")
	if err != nil {
		panic(err)
	}
	if embeddedBroker != nil {
		natsCfg.Options.InProcessServer = embeddedBroker.InProcessServer()
		natsCfg.Options.Servers = []string{embeddedBroker.ClientURL()}
	}
	bus, err := rpcnats.NewBus(natsCfg)
	if err != nil {
		panic(err)
	}
	closeComponents = append(closeComponents, entrypoint.Component{Name: "rpc-bus", Close: bus.Close})

	accountApp, err := app.NewAccountApp()
	if err != nil {
		panic(err)
	}
	proxyApp, err := app.NewProxyApp(bus, accountApp.Verifier, fileCfg.Proxy)
	if err != nil {
		panic(err)
	}
	closeComponents = append(closeComponents, entrypoint.Component{Name: "proxy", Close: func(context.Context) error {
		proxyApp.Close()
		return nil
	}})

	server := app.NewWebSocketHTTPServer(*addr, proxyApp.Handler)

	fmt.Printf("minigame proxy listening on ws://%s/ws\n", *addr)
	if err := app.ServeHTTP(ctx, server); err != nil {
		panic(err)
	}
}

func proxyBrokerConfig(cfg app.ProxyBrokerFileConfig) proxybroker.Config {
	return proxybroker.Config{
		ConfigFile:      cfg.ConfigFile,
		ReadyTimeout:    cfg.ReadyTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}
}
