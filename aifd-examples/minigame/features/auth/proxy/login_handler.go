package proxy

import (
	"context"
	"fmt"
	"strconv"
	"time"

	account "github.com/li9chuan/aifirstdev/aifd-account"
	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	proxyc "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

type SessionValidator interface {
	ValidateSession(context.Context, account.SessionRef) (account.LoginSession, error)
}

// Revalidator queries the same authority used during initial authentication.
type Revalidator struct{ Sessions SessionValidator }

func (r Revalidator) Revalidate(ctx context.Context, a proxyc.AuthContext) error {
	if r.Sessions == nil || a.BackendID != auth.AccountBackendID {
		return fmt.Errorf("invalid account backend")
	}
	id, err := strconv.ParseInt(a.Account, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != a.Account {
		return fmt.Errorf("invalid account")
	}
	session, err := r.Sessions.ValidateSession(ctx, account.SessionRef{AccountID: id, SessionID: a.SessionID})
	if err != nil {
		return err
	}
	if !session.ExpiresAt.Equal(a.ValidUntil) {
		return fmt.Errorf("session deadline changed")
	}
	return nil
}

// Authenticate verifies a signed account payload and asks game host to activate the player root.
func Authenticate(bus rpc.Bus, verifier *token.Verifier, sessions SessionValidator) proxyc.TypedLoginFunc[*pbmsg.MceAuth, *pbmsg.MseAuth] {
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
		if sessions == nil || payload.GetBackendId() != auth.AccountBackendID || payload.GetAudience() != auth.AccountAudience {
			return proxyc.Identity{}, failRsp(auth.RetInvalidAuth)
		}
		validated, err := sessions.ValidateSession(ctx, account.SessionRef{AccountID: payload.GetAccountId(), SessionID: payload.GetSessionId()})
		if err != nil {
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
			UID:       payload.GetUid(),
			Account:   strconv.FormatInt(payload.GetAccountId(), 10),
			ServerID:  payload.GetRealmId(),
			BackendID: payload.GetBackendId(), SessionID: payload.GetSessionId(), ValidUntil: validated.ExpiresAt,
		}, &rsp
	}
}

func failRsp(ret int32) *pbmsg.MseAuth {
	return &pbmsg.MseAuth{Ret: ret}
}
