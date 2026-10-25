package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/internal/redistest"
	mailuser "github.com/li9chuan/aifirstdev/aifd-plugins/mail/user"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	natsbus "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	runtimeuser "github.com/li9chuan/aifirstdev/aifd-runtime/user"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	redis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestMailOwnerHelper is the independent owner process used below. It has no
// simulator dependency and calls an ordinary, deliberately non-idempotent Game.
func TestMailOwnerHelper(t *testing.T) {
	if os.Getenv("AIFD_MAIL_OWNER_HELPER") != "1" {
		return
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("AIFD_MAIL_OWNER_REDIS")})
	defer client.Close()
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	bus, err := natsbus.NewBus(natsbus.Config{Options: natsgo.Options{Servers: []string{os.Getenv("AIFD_MAIL_OWNER_NATS")}, Name: "mail-owner"}})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close(ctx)
	owner, err := NewModule(Options{DB: db, Prefix: os.Getenv("AIFD_MAIL_OWNER_PREFIX"), ClaimCD: time.Second, AwardTimeout: 200 * time.Millisecond,
		Awarder: mail.AwarderFunc(func(ctx context.Context, uid int64, data *mailpb.MailData) error {
			deadline, _ := ctx.Deadline()
			return bus.RequestPB(ctx, uid, "Game.Award", data, &emptypb.Empty{}, contracts.RequestOptions{Timeout: time.Until(deadline)})
		})})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	if err := owner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	reg := rpc.NewRegistry()
	if err := owner.RegisterRPC(reg); err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{}, 1)
	if err := rpc.Server(reg, "Mail.TestReady", func(s *rpc.Session, _ *emptypb.Empty) { s.Response(&emptypb.Empty{}) }); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Server(reg, "Mail.TestQuit", func(s *rpc.Session, _ *emptypb.Empty) { s.Response(&emptypb.Empty{}); quit <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Install(bus, reg); err != nil {
		t.Fatal(err)
	}
	go rpcruntime.Run(ctx, bus.Deliveries(), rpcruntime.RunnerOptions{Workers: 1})
	<-quit
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type ownerProcess struct {
	cmd  *exec.Cmd
	done chan error
	log  *os.File
	bus  contracts.Bus
}

func startOwnerProcess(t *testing.T, bus contracts.Bus, natsURL, redisAddr, prefix string) *ownerProcess {
	t.Helper()
	log, err := os.Create(filepath.Join(t.TempDir(), "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	child := &ownerProcess{bus: bus, log: log, done: make(chan error, 1)}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMailOwnerHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "AIFD_MAIL_OWNER_HELPER=1", "AIFD_MAIL_OWNER_REDIS="+redisAddr, "AIFD_MAIL_OWNER_NATS="+natsURL, "AIFD_MAIL_OWNER_PREFIX="+prefix)
	cmd.Stdout = log
	cmd.Stderr = log
	hideTestProcess(cmd)
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	child.cmd = cmd
	go func() { child.done <- cmd.Wait() }()
	t.Cleanup(func() { child.stop(t) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		err := bus.RequestPB(ctx, 0, "Mail.TestReady", &emptypb.Empty{}, &emptypb.Empty{}, contracts.RequestOptions{Timeout: 100 * time.Millisecond})
		if err == nil {
			return child
		}
		select {
		case exitErr := <-child.done:
			child.cmd = nil
			log.Close()
			raw, _ := os.ReadFile(log.Name())
			t.Fatalf("owner exited: %v\n%s", exitErr, raw)
		case <-ctx.Done():
			t.Fatal("owner readiness timeout", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (p *ownerProcess) stop(t *testing.T) {
	t.Helper()
	if p.cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.bus.RequestPB(ctx, 0, "Mail.TestQuit", &emptypb.Empty{}, &emptypb.Empty{}, contracts.RequestOptions{Timeout: time.Second})
	if err != nil {
		p.cmd.Process.Kill()
	}
	select {
	case exitErr := <-p.done:
		if err != nil || exitErr != nil {
			raw, _ := os.ReadFile(p.log.Name())
			t.Errorf("owner shutdown: request=%v process=%v\n%s", err, exitErr, raw)
		}
	case <-ctx.Done():
		p.cmd.Process.Kill()
		<-p.done
		t.Error("owner shutdown deadline")
	}
	p.log.Close()
	p.cmd = nil
}

func TestRealRedisNATSOwnerProcessRestartAndTimeoutRetry(t *testing.T) {
	redisServer := redistest.Start(t)
	server, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS not ready")
	}
	defer server.Shutdown()
	ctx := context.Background()
	gameBus, err := natsbus.NewBus(natsbus.Config{Options: natsgo.Options{Servers: []string{server.ClientURL()}, Name: "game"}})
	if err != nil {
		t.Fatal(err)
	}
	defer gameBus.Close(ctx)
	caller, err := natsbus.NewBus(natsbus.Config{Options: natsgo.Options{Servers: []string{server.ClientURL()}, Name: "client-test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close(ctx)
	gameDB := rdb.MustNewMemoryDB()
	defer gameDB.Close(ctx)
	var level atomic.Int32
	var awards atomic.Int64
	var late atomic.Pointer[rpc.Session]
	adapter := mailuser.NewModule[*emptypb.Empty](mailuser.Options[*emptypb.Empty]{GroupResolver: mailuser.GroupResolverFunc[*emptypb.Empty](func(context.Context, *runtimeuser.User[*emptypb.Empty]) ([]string, error) {
		return []string{fmt.Sprintf("L%d", level.Load()), "server:1"}, nil
	})})
	store, err := runtimeuser.NewStore(runtimeuser.StoreOptions[*emptypb.Empty]{DB: gameDB, Key: func(uid int64) string { return fmt.Sprintf("game:user:%d", uid) }, New: func(int64) *emptypb.Empty { return &emptypb.Empty{} }})
	if err != nil {
		t.Fatal(err)
	}
	reg := rpc.NewRegistry()
	rt := runtimeuser.NewRuntime(reg, store, runtimeuser.NewManager[*emptypb.Empty](adapter), runtimeuser.WithBus(gameBus))
	defer rt.Close(ctx)
	if _, err := rt.CreateUser(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Server(reg, "Game.Award", func(s *rpc.Session, _ *mailpb.MailData) {
		// First operation really executes but loses its response. The second executes
		// again; there is no Game receipt or deduplication state.
		if awards.Add(1) == 1 {
			late.Store(s)
			return
		}
		if old := late.Swap(nil); old != nil {
			old.Response(&emptypb.Empty{})
		}
		s.Response(&emptypb.Empty{})
	}); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Install(gameBus, reg); err != nil {
		t.Fatal(err)
	}
	go rpcruntime.Run(ctx, gameBus.Deliveries(), rpcruntime.RunnerOptions{Workers: 1})
	if err := proxyruntime.NewRPCSessionLifecycle(gameBus).OnConnect(ctx, proxyruntime.Session{ProxyInstanceID: "test-boot", ConnectionID: "A", Identity: proxy.Identity{UID: 42}}); err != nil {
		t.Fatal(err)
	}
	prefix := "mail:process"
	owner := startOwnerProcess(t, caller, server.ClientURL(), redisServer.Addr, prefix)
	add := func(req *mailpb.MailAddRequest) int64 {
		t.Helper()
		var rsp mailpb.MailAddResponse
		if err := caller.RequestPB(ctx, 0, mail.TopicMailAdd, req, &rsp, contracts.RequestOptions{Timeout: time.Second}); err != nil || rsp.GetRet() != mail.RetOK {
			t.Fatal(rsp.String(), err)
		}
		return rsp.GetMailIdList()[0]
	}
	personalID := add(personal(42, true))
	var added mailpb.MailAddResponse
	if err := caller.RequestPB(ctx, 0, mail.TopicMailAddBatch, &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{shared("L0"), shared("server:1")}}, &added, contracts.RequestOptions{Timeout: time.Second}); err != nil || added.GetRet() != mail.RetOK || len(added.GetMailIdList()) != 2 {
		t.Fatal(added.String(), err)
	}
	sharedID, serverID := added.MailIdList[0], added.MailIdList[1]
	call := func(topic string, req, rsp proto.Message) {
		t.Helper()
		msg, err := contracts.NewMessagePB(topic, req)
		if err != nil {
			t.Fatal(err)
		}
		msg.Header[proxy.HeaderCallerKind] = proxy.CallerClient
		msg.Header[proxy.HeaderProxyInstanceID] = "test-boot"
		msg.Header[proxy.HeaderConnectionID] = "A"
		result, err := caller.Request(ctx, 42, msg, contracts.RequestOptions{Timeout: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.StatusFromMessage(result); err != nil {
			t.Fatal(err)
		}
		if err := result.DecodePB(rsp); err != nil {
			t.Fatal(err)
		}
	}
	var listed mailpb.MseMailList
	call(mail.TopicMceMailList, &mailpb.MceMailList{}, &listed)
	if len(listed.GetMailList()) != 3 {
		t.Fatal(listed.String())
	}
	var claimed mailpb.MseMailClaim
	call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{personalID}}, &claimed)
	if claimed.GetRet() != mail.RetTimeout || awards.Load() != 1 {
		t.Fatal(claimed.String(), awards.Load())
	}
	retryAt := claimed.GetRetryAtUnixMs()
	call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{personalID}}, &claimed)
	if claimed.GetRet() != mail.RetBusy || awards.Load() != 1 {
		t.Fatal("CD did not block", claimed.String(), awards.Load())
	}
	if wait := time.Until(time.UnixMilli(retryAt)) + 5*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
	call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{personalID}}, &claimed)
	if claimed.GetRet() != mail.RetOK || awards.Load() != 2 {
		t.Fatal("ordinary RPC retry/Forward failed", claimed.String(), awards.Load())
	}
	var read mailpb.MseMailRead
	call(mail.TopicMceMailRead, &mailpb.MceMailRead{MailIdList: []int64{sharedID}}, &read)
	var deleted mailpb.MseMailDeleteRead
	call(mail.TopicMceMailDeleteRead, &mailpb.MceMailDeleteRead{MailIdList: []int64{sharedID}}, &deleted)
	if len(deleted.GetMailIdList()) != 1 {
		t.Fatal(deleted.String())
	}
	owner.stop(t)
	redisServer.Restart(t)
	level.Store(1)
	owner = startOwnerProcess(t, caller, server.ClientURL(), redisServer.Addr, prefix)
	call(mail.TopicMceMailList, &mailpb.MceMailList{}, &listed)
	if len(listed.GetMailList()) != 2 || listed.GetMailList()[0].GetMailId() != personalID || !listed.GetMailList()[0].GetClaimed() || listed.GetMailList()[1].GetMailId() != serverID {
		t.Fatal("restart state mismatch", listed.String())
	}
	level.Store(0)
	call(mail.TopicMceMailList, &mailpb.MceMailList{}, &listed)
	if len(listed.GetMailList()) != 2 || listed.GetMailList()[1].GetMailId() != serverID {
		t.Fatal("deleted L0 mail resurrected on rejoining after restart", listed.String())
	}
	call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{personalID}}, &claimed)
	if claimed.GetRet() != mail.RetOK || awards.Load() != 2 {
		t.Fatal("completed claim retried after restart")
	}
	owner.stop(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr})
	defer client.Close()
	if client.HExists(ctx, prefix+":user:42:data", fmt.Sprint(sharedID)).Val() {
		t.Fatal("shared delete not persisted")
	}
	raw, err := client.HGet(ctx, prefix+":user:42:meta", "meta").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	meta := &mailpb.MailboxMeta{}
	if err := proto.Unmarshal(raw, meta); err != nil || len(meta.GetGroupCursors()) != 2 {
		t.Fatal("per-group cursors not recovered", meta, err)
	}
	t.Logf("independent owner + single Game worker: 2 ordinary awards after lost reply, deleted shared mail stayed deleted across owner/Redis restart")
}
