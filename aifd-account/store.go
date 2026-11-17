package account

import (
	"context"
	"time"
)

type OpenSessionRequest struct {
	Identity  LoginRef
	SessionID string
	ExpiresAt time.Time
}

type IdentityWrite struct {
	Actor            SessionRef
	Reauthenticated  LoginRef
	Target           LoginRef
	ExpectedRevision uint64
}

type Store interface {
	CreateAccount(context.Context, LoginRef) (AccountSnapshot, error)
	GetAccount(context.Context, int64) (AccountSnapshot, error)
	ResolveAccount(context.Context, LoginRef) (AccountSnapshot, error)

	OpenSession(context.Context, OpenSessionRequest) (LoginSession, error)
	BindIdentity(context.Context, IdentityWrite) (uint64, error)
	UnbindIdentity(context.Context, IdentityWrite) (uint64, error)

	ValidateSession(context.Context, SessionRef) (LoginSession, error)
	RevokeSession(context.Context, SessionRef) error
	AdvanceSessionEpoch(context.Context, int64, uint64) (uint64, error)
	ChangeAccountStatus(context.Context, StatusWrite) (AccountSnapshot, error)
}
