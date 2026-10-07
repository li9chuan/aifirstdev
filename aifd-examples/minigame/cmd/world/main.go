package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
	observememory "github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"github.com/li9chuan/aifirstdev/aifd-runtime/entrypoint"
	runtimepprof "github.com/li9chuan/aifirstdev/aifd-runtime/pprof"
)

func main() {
	confPath := flag.String("conf", "", "shared service config path")
	natsURL := flag.String("nats", "", "proxy-broker NATS client URL override")
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

	db, err := rdb.NewMemoryDB()
	if err != nil {
		panic(err)
	}

	reg := rpc.NewRegistry()
	observe := observememory.NewRecorder()
	worldApp, err := app.NewWorldApp(ctx, reg, observe, app.WithWorldDB(db))
	if err != nil {
		panic(err)
	}

	bus, err := app.NewNATSBusWithConfig(fileCfg.RPC.NATS, *natsURL, "minigame-world")
	if err != nil {
		panic(err)
	}
	defer entrypoint.Close(context.Background(),
		entrypoint.Component{Name: "db", Close: db.Close},
		entrypoint.Component{Name: "world", Close: worldApp.Close},
		entrypoint.Component{Name: "rpc-bus", Close: bus.Close},
	)
	if err := rpc.Start(ctx, bus, reg); err != nil {
		panic(err)
	}

	fmt.Println("minigame world host registered counter routes")
	<-ctx.Done()
}
