package user

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	mailservice "github.com/li9chuan/aifirstdev/aifd-plugins/mail/service"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	runtimeuser "github.com/li9chuan/aifirstdev/aifd-runtime/user"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestClientForwardUsesTrustedGroupsOnEveryRouteWithOneWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := rdb.MustNewMemoryDB()
	defer db.Close(context.Background())
	bus := rpc.NewLocalBus(rpc.LocalConfig{})
	defer bus.Close(context.Background())
	var group atomic.Int32
	var resolverError atomic.Bool
	var awards atomic.Int64
	adapter := NewModule[*emptypb.Empty](Options[*emptypb.Empty]{GroupResolver: GroupResolverFunc[*emptypb.Empty](func(context.Context, *runtimeuser.User[*emptypb.Empty]) ([]string, error) {
		if resolverError.Load() {
			return nil, errors.New("membership unavailable")
		}
		if group.Load() == 1 {
			return []string{"L1"}, nil
		}
		return []string{"L0"}, nil
	})})
	store, err := runtimeuser.NewStore(runtimeuser.StoreOptions[*emptypb.Empty]{DB: db, Key: func(uid int64) string { return fmt.Sprintf("user:%d", uid) }, New: func(int64) *emptypb.Empty { return &emptypb.Empty{} }})
	if err != nil {
		t.Fatal(err)
	}
	reg := rpc.NewRegistry()
	rt := runtimeuser.NewRuntime(reg, store, runtimeuser.NewManager[*emptypb.Empty](adapter), runtimeuser.WithBus(bus))
	defer rt.Close(context.Background())
	if _, err := rt.CreateUser(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	owner, err := mailservice.NewModule(mailservice.Options{DB: db, Awarder: mail.AwarderFunc(func(ctx context.Context, uid int64, data *mailpb.MailData) error {
		if _, ok := rt.GetUser(uid); !ok {
			return errors.New("user absent")
		}
		awards.Add(1)
		return nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	if err := owner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.RegisterRPC(reg); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Install(bus, reg); err != nil {
		t.Fatal(err)
	}
	go rpcruntime.Run(ctx, bus.Deliveries(), rpcruntime.RunnerOptions{Workers: 1})
	lifecycle := proxyruntime.NewRPCSessionLifecycle(bus)
	if err := lifecycle.OnConnect(ctx, proxyruntime.Session{ProxyInstanceID: "boot", ConnectionID: "A", Identity: proxy.Identity{UID: 7}}); err != nil {
		t.Fatal(err)
	}
	call := func(topic string, request, response proto.Message) error {
		msg, err := contracts.NewMessagePB(topic, request)
		if err != nil {
			return err
		}
		msg.Header[proxy.HeaderCallerKind] = proxy.CallerClient
		msg.Header[proxy.HeaderProxyInstanceID] = "boot"
		msg.Header[proxy.HeaderConnectionID] = "A"
		out, err := bus.Request(ctx, 7, msg, contracts.RequestOptions{Timeout: time.Second})
		if err != nil {
			return err
		}
		if err := contracts.StatusFromMessage(out); err != nil {
			return err
		}
		return out.DecodePB(response)
	}
	added := owner.Add(ctx, &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "L1",
		Base: &mailpb.MailBase{TemplateId: 1, ItemList: []*mailpb.MailItem{{Tid: 1, Num: 1}}}})
	if added.GetRet() != mail.RetOK {
		t.Fatal(added.String())
	}
	id := added.GetMailIdList()[0]
	var list mailpb.MseMailList
	if err := call(mail.TopicMceMailList, &mailpb.MceMailList{GroupKeys: []string{"L1"}}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.GetMailList()) != 0 {
		t.Fatal("client group widened resolver audience")
	}
	group.Store(1)
	if err := call(mail.TopicMceMailList, &mailpb.MceMailList{}, &list); err != nil || len(list.GetMailList()) != 1 {
		t.Fatal(list.String(), err)
	}
	group.Store(0)
	var detail mailpb.MseMailGet
	if err := call(mail.TopicMceMailGet, &mailpb.MceMailGet{MailId: id}, &detail); err != nil || detail.GetRet() != mail.RetNotFound {
		t.Fatal(detail.String(), err)
	}
	var claim mailpb.MseMailClaim
	var read mailpb.MseMailRead
	if err := call(mail.TopicMceMailRead, &mailpb.MceMailRead{MailIdList: []int64{id}}, &read); err != nil || len(read.GetMailIdList()) != 0 {
		t.Fatal(read.String(), err)
	}
	var deleted mailpb.MseMailDeleteRead
	if err := call(mail.TopicMceMailDeleteRead, &mailpb.MceMailDeleteRead{MailIdList: []int64{id}}, &deleted); err != nil || len(deleted.GetMailIdList()) != 0 {
		t.Fatal(deleted.String(), err)
	}
	if err := call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{id}}, &claim); err != nil || claim.GetRet() != mail.RetNotFound || awards.Load() != 0 {
		t.Fatal(claim.String(), err)
	}
	group.Store(1)
	if err := call(mail.TopicMceMailClaim, &mailpb.MceMailClaim{MailIdList: []int64{id}}, &claim); err != nil || claim.GetRet() != mail.RetOK || awards.Load() != 1 {
		t.Fatal("Forward deadlocked or failed", claim.String(), err)
	}
	group.Store(0)
	if err := call(mail.TopicMceMailDeleteRead, &mailpb.MceMailDeleteRead{MailIdList: []int64{id}}, &deleted); err != nil || len(deleted.GetMailIdList()) != 0 {
		t.Fatal("departed member deleted a claimed group mail", deleted.String(), err)
	}
	group.Store(1)
	if err := call(mail.TopicMceMailGet, &mailpb.MceMailGet{MailId: id}, &detail); err != nil || detail.GetRet() != mail.RetOK || !detail.GetMail().GetClaimed() {
		t.Fatal("rejoining lost claimed mail", detail.String(), err)
	}
	resolverError.Store(true)
	if err := call(mail.TopicMceMailList, &mailpb.MceMailList{}, &list); err == nil {
		t.Fatal("resolver error admitted")
	}
}

func TestResolverDefaultAndCopyOwnership(t *testing.T) {
	module := NewModule[*emptypb.Empty](Options[*emptypb.Empty]{})
	groups, err := module.groupKeys(context.Background(), nil)
	if err != nil || len(groups) != 0 {
		t.Fatal(groups, err)
	}
	configured := []string{"L0"}
	module.resolver = GroupResolverFunc[*emptypb.Empty](func(context.Context, *runtimeuser.User[*emptypb.Empty]) ([]string, error) {
		return configured, nil
	})
	groups, err = module.groupKeys(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	groups[0] = "forged"
	if configured[0] != "L0" {
		t.Fatal("resolver's mutable state escaped")
	}
	if err := module.RegisterRPC(rpc.NewRegistry()); err == nil {
		t.Fatal("unbound runtime registered")
	}
}
