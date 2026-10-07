package app

import (
	"strconv"

	authgame "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/game"
	countergame "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter/game"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeroot "github.com/li9chuan/aifirstdev/aifd-runtime/root"
)

// GameApp 持有 minigame 游戏侧 root runtime 和业务 RPC 注册表。
type GameApp struct {
	Registry *rpc.Registry
	Runtime  *runtimeroot.Runtime[*pbmsg.DBPlayer]
	Counter  *countergame.Module
}

type gameOptions struct {
	db      *rdb.DB
	config  countergame.Config
	observe observe.Sink
}

// GameOption 配置 minigame game host。
type GameOption func(*gameOptions)

// WithGameConfig 传入业务解释后的运行期配置。
func WithGameConfig(config countergame.Config) GameOption {
	return func(opts *gameOptions) {
		opts.config = config
	}
}

// WithGameObserve 传入 observe sink。
func WithGameObserve(sink observe.Sink) GameOption {
	return func(opts *gameOptions) {
		opts.observe = sink
	}
}

// WithGameDB 传入游戏根文档使用的显式 DB。
func WithGameDB(db *rdb.DB) GameOption {
	return func(opts *gameOptions) {
		opts.db = db
	}
}

// NewGameApp 创建 game host，并注册登录确认 route。
func NewGameApp(reg *rpc.Registry, options ...GameOption) (*GameApp, error) {
	if reg == nil {
		reg = rpc.NewRegistry()
	}
	var opts gameOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	store, err := runtimeroot.NewStore(runtimeroot.StoreOptions[*pbmsg.DBPlayer]{
		DB:  opts.db,
		Key: gameRootKey,
		New: newDBPlayer,
	})
	if err != nil {
		return nil, err
	}
	counterModule := countergame.NewModule(countergame.Options{
		Config:  opts.config,
		Observe: opts.observe,
	})
	modules := runtimeroot.NewManager[*pbmsg.DBPlayer](counterModule)
	runtime := runtimeroot.NewRuntime(reg, store, modules)
	if err := runtime.RegisterRPC(); err != nil {
		return nil, err
	}
	if err := authgame.RegisterRPC(reg, runtime); err != nil {
		return nil, err
	}
	return &GameApp{Registry: reg, Runtime: runtime, Counter: counterModule}, nil
}

func gameRootKey(rootID int64) string {
	return "minigame:game:" + strconv.FormatInt(rootID, 10)
}

func newDBPlayer(rootID int64) *pbmsg.DBPlayer {
	return &pbmsg.DBPlayer{Uid: rootID}
}
