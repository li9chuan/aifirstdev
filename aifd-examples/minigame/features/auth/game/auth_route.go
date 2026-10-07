package game

import (
	"fmt"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeroot "github.com/li9chuan/aifirstdev/aifd-runtime/root"
	"google.golang.org/protobuf/proto"
)

// RegisterRPC 注册 game host 登录确认 route。
func RegisterRPC(reg *rpc.Registry, service *runtimeroot.Runtime[*pbmsg.DBPlayer]) error {
	if service == nil {
		return fmt.Errorf("game runtime is nil")
	}
	return rpc.Server(reg, auth.TopicGameAuthOK, func(s *rpc.Session, req *pbmsg.MceAuth) {
		payload := req.GetPayload()
		if payload.GetUid() <= 0 {
			s.ResponseAs(auth.TopicMseAuth, &pbmsg.MseAuth{Ret: auth.RetInvalidAuth})
			return
		}
		owner, isNew, err := service.Activate(s.Context(), payload.GetUid())
		if err != nil {
			s.ResponseAs(auth.TopicMseAuth, &pbmsg.MseAuth{Ret: auth.RetGameFailed})
			return
		}
		if err := owner.Mutate(s.Context(), func(db *pbmsg.DBPlayer) error {
			if db.GetUid() == 0 {
				db.Uid = payload.GetUid()
			}
			if isNew && db.GetRegisterTime() == 0 {
				db.RegisterTime = time.Now().Unix()
			}
			db.LoginCount++
			db.LastSession = payload.GetTokenId()
			db.Auth = proto.Clone(payload).(*pbmsg.MAuthPayload)
			return nil
		}); err != nil {
			s.ResponseAs(auth.TopicMseAuth, &pbmsg.MseAuth{Ret: auth.RetGameFailed})
			return
		}
		s.ResponseAs(auth.TopicMseAuth, &pbmsg.MseAuth{Ret: auth.RetOK})
	})
}
