package app

import (
	"context"
	"time"

	observememory "github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

// AllInOneApp 是无外部依赖的 all-in-one 全链路示例。
type AllInOneApp struct {
	cancel context.CancelFunc

	DB      *rdb.DB
	Bus     *rpc.LocalBus
	Account *AccountApp
	Config  *ConfigApp
	Observe *observememory.Recorder
	Game    *GameApp
	World   *WorldApp
	Proxy   *ProxyApp
}

// NewAllInOneApp 创建配置、observe、world host、game host、local bus 和 proxy handler，并启动 RPC runner。
func NewAllInOneApp(ctx context.Context, configs ...ServiceFileConfig) (*AllInOneApp, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	db, err := rdb.NewMemoryDB()
	if err != nil {
		cancel()
		return nil, err
	}
	configApp, err := NewConfigApp(runCtx)
	if err != nil {
		cancel()
		_ = db.Close(context.Background())
		return nil, err
	}
	accountApp, err := NewAccountApp()
	if err != nil {
		cancel()
		_ = db.Close(context.Background())
		return nil, err
	}
	observe := observememory.NewRecorder()
	reg := rpc.NewRegistry()
	worldApp, err := NewWorldApp(runCtx, reg, observe, WithWorldDB(db))
	if err != nil {
		cancel()
		_ = db.Close(context.Background())
		return nil, err
	}
	gameApp, err := NewGameApp(reg, WithGameDB(db), WithGameConfig(configApp), WithGameObserve(observe))
	if err != nil {
		cancel()
		_ = worldApp.Close(context.Background())
		_ = db.Close(context.Background())
		return nil, err
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{
		ServiceName:    "minigame",
		Buffer:         32,
		DefaultTimeout: time.Second,
	})
	if err := rpc.Start(runCtx, bus, reg); err != nil {
		cancel()
		_ = bus.Close(context.Background())
		_ = worldApp.Close(context.Background())
		_ = db.Close(context.Background())
		return nil, err
	}

	var fileCfg ServiceFileConfig
	if len(configs) > 0 {
		fileCfg = configs[0]
	}
	proxyApp, err := NewProxyApp(bus, accountApp.Verifier, fileCfg.Proxy)
	if err != nil {
		cancel()
		_ = bus.Close(context.Background())
		_ = worldApp.Close(context.Background())
		_ = db.Close(context.Background())
		return nil, err
	}
	return &AllInOneApp{cancel: cancel, DB: db, Bus: bus, Account: accountApp, Config: configApp, Observe: observe, Game: gameApp, World: worldApp, Proxy: proxyApp}, nil
}

// Close 停止 all-in-one runner 并关闭 bus。
func (a *AllInOneApp) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.Proxy != nil {
		a.Proxy.Close()
	}
	if a.Bus != nil {
		if err := a.Bus.Close(ctx); err != nil {
			return err
		}
	}
	if a.World != nil {
		if err := a.World.Close(ctx); err != nil {
			return err
		}
	}
	if a.DB != nil {
		return a.DB.Close(ctx)
	}
	return nil
}
