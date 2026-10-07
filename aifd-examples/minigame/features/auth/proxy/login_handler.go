package proxy

import (
	"context"
	"strconv"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	proxyc "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

// Authenticate verifies a signed account payload and asks game host to activate the player root.
func Authenticate(bus rpc.Bus, verifier *token.Verifier) proxyc.TypedLoginFunc[*pbmsg.MceAuth, *pbmsg.MseAuth] {
	return func(ctx context.Context, _ proxyc.LoginContext, login *pbmsg.MceAuth) (proxyc.Identity, *pbmsg.MseAuth) {
		if verifier == nil {
			return proxyc.Identity{}, failRsp(auth.RetInvalidAuth)
		}
		payload := login.GetPayload()
		payloadBytes, err := auth.CanonicalPayloadBytes(payload)
		if err != nil {
			return proxyc.Identity{}, failRsp(auth.RetInvalidAuth)
		}
		if err := verifier.Verify(payloadBytes, login.GetToken()); err != nil {
			return proxyc.Identity{}, failRsp(auth.RetInvalidAuth)
		}
		if payload.GetAccountId() <= 0 || payload.GetUid() <= 0 || payload.GetRealmId() <= 0 {
			return proxyc.Identity{}, failRsp(auth.RetInvalidAuth)
		}
		if bus == nil {
			return proxyc.Identity{}, failRsp(auth.RetGameFailed)
		}

		var rsp pbmsg.MseAuth
		if err := bus.RequestPB(ctx, payload.GetUid(), auth.TopicGameAuthOK, login, &rsp, rpc.RequestOptions{Timeout: time.Second}); err != nil {
			return proxyc.Identity{}, failRsp(auth.RetGameFailed)
		}
		if rsp.GetRet() != auth.RetOK {
			return proxyc.Identity{}, &rsp
		}
		return proxyc.Identity{
			UID:      payload.GetUid(),
			Account:  strconv.FormatInt(payload.GetAccountId(), 10),
			ServerID: payload.GetRealmId(),
			Claims: map[string]string{
				"provider":     payload.GetProvider(),
				"subject_hash": payload.GetSubjectHash(),
			},
		}, &rsp
	}
}

func failRsp(ret int32) *pbmsg.MseAuth {
	return &pbmsg.MseAuth{Ret: ret}
}
