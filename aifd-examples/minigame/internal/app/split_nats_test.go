package app

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	authrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/robot"
	counterrobot "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter/robot"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	observememory "github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	proxybroker "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats/broker"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
)

func TestSplitModeEmbeddedProxyBrokerRobotCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	embeddedBroker, err := proxybroker.Start(ctx, proxybroker.Config{
		ConfigFile:      writeNATSBrokerConfig(t),
		ReadyTimeout:    time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("broker Start error = %v", err)
	}
	defer embeddedBroker.Close(context.Background())
	natsURL := embeddedBroker.ClientURL()

	ownerBus, err := NewNATSBus(natsURL, "minigame-test-embedded-owner")
	if err != nil {
		t.Fatalf("NewNATSBus owner error = %v", err)
	}
	defer ownerBus.Close(context.Background())
	globalBus, err := NewNATSBus(natsURL, "minigame-test-embedded-global")
	if err != nil {
		t.Fatalf("NewNATSBus global error = %v", err)
	}
	defer globalBus.Close(context.Background())
	proxyCfg, err := (ServiceFileConfig{}).NATSConfig("minigame-test-embedded-proxy", "")
	if err != nil {
		t.Fatalf("NATSConfig proxy error = %v", err)
	}
	proxyCfg.Options.InProcessServer = embeddedBroker.InProcessServer()
	proxyCfg.Options.Servers = []string{embeddedBroker.ClientURL()}
	proxyBus, err := rpcnats.NewBus(proxyCfg)
	if err != nil {
		t.Fatalf("NewBus proxy error = %v", err)
	}
	defer proxyBus.Close(context.Background())

	runSplitModeRobotCounter(t, ctx, ownerBus, globalBus, proxyBus)
}

func writeNATSBrokerConfig(t *testing.T) string {
	t.Helper()
	configFile := filepath.Join(t.TempDir(), "nats.conf")
	config := []byte("server_name: minigame-test-proxy-broker\nhost: 127.0.0.1\nport: -1\n")
	if err := os.WriteFile(configFile, config, 0600); err != nil {
		t.Fatalf("WriteFile broker config error = %v", err)
	}
	return configFile
}

func runSplitModeRobotCounter(t *testing.T, ctx context.Context, ownerBus rpc.Bus, globalBus rpc.Bus, proxyBus rpc.Bus) {
	t.Helper()
	ownerDB := rdb.MustNewMemoryDB()
	defer ownerDB.Close(context.Background())
	globalDB := rdb.MustNewMemoryDB()
	defer globalDB.Close(context.Background())

	ownerReg := rpc.NewRegistry()
	globalReg := rpc.NewRegistry()
	configApp, err := NewConfigApp(ctx)
	if err != nil {
		t.Fatalf("NewConfigApp error = %v", err)
	}
	observe := observememory.NewRecorder()

	worldApp, err := NewWorldApp(ctx, globalReg, observe, WithWorldDB(globalDB))
	if err != nil {
		t.Fatalf("NewWorldApp error = %v", err)
	}
	defer worldApp.Close(context.Background())

	gameApp, err := NewGameApp(ownerReg, WithGameDB(ownerDB), WithGameConfig(configApp), WithGameObserve(observe))
	if err != nil {
		t.Fatalf("NewGameApp error = %v", err)
	}

	if err := rpc.Start(ctx, ownerBus, ownerReg); err != nil {
		t.Fatalf("rpc.Start owner error = %v", err)
	}

	if err := rpc.Start(ctx, globalBus, globalReg); err != nil {
		t.Fatalf("rpc.Start global error = %v", err)
	}

	accountApp, err := NewAccountApp()
	if err != nil {
		t.Fatalf("NewAccountApp error = %v", err)
	}
	proxyApp, err := NewProxyApp(proxyBus, accountApp.Verifier)
	if err != nil {
		t.Fatalf("NewProxyApp error = %v", err)
	}
	defer proxyApp.Close()
	server := httptest.NewServer(proxyApp.Handler)
	defer server.Close()

	authModules := map[int64]*authrobot.AuthModule{}
	counterModules := map[int64]*counterrobot.CounterModule{}
	group := newRobotCounterGroup(t, ctx, server.URL, 1, 3001, accountApp.DemoAuth, authModules, counterModules)
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
		if counterRsp.GetValue() != 2 {
			t.Fatalf("robot uid %d counter value = %d, want 2", uid, counterRsp.GetValue())
		}
		if counterRsp.GetUid() <= 0 {
			t.Fatalf("robot uid %d counter rsp uid = %d, want positive uid", uid, counterRsp.GetUid())
		}
		waitForSplitRoot(t, gameApp, counterRsp.GetUid())
	}
	if err := globalDB.Flush(context.Background()); err != nil {
		t.Fatalf("global DB Flush error = %v", err)
	}
	global := waitForCounterWorldTotal(t, worldApp, 2)
	if global.GetTotalAdd() != 2 {
		t.Fatalf("global total add = %d, want 2", global.GetTotalAdd())
	}
}

func waitForSplitRoot(t *testing.T, app *GameApp, uid int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := app.Runtime.GetRoot(uid); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("root %d is not active in split game host", uid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForCounterWorldTotal(t *testing.T, app *WorldApp, want int64) *pbmsg.CounterWorldState {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		global := app.Counter.Snapshot()
		if global.GetTotalAdd() >= want || time.Now().After(deadline) {
			return global
		}
		time.Sleep(5 * time.Millisecond)
	}
}
