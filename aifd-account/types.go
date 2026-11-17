package account

import (
	"context"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

type AccountStatus uint8

const (
	AccountStatusUnspecified AccountStatus = iota
	AccountStatusActive
	AccountStatusDisabled
)

type LoginRef struct {
	AuthorityID   string
	SubjectDigest [32]byte
}

type SessionRef struct {
	AccountID int64
	SessionID string
}

type AccountSnapshot struct {
	AccountID        int64
	Status           AccountStatus
	Identities       []LoginRef
	IdentityRevision uint64
	SessionEpoch     uint64
}

type LoginSession struct {
	Ref          SessionRef
	SessionEpoch uint64
	ExpiresAt    time.Time
}

const (
	VerifyRegister      = "register"
	VerifyLogin         = "login"
	VerifyBindCandidate = "bind_candidate"
	VerifyBindCurrent   = "bind_current"
	VerifyUnbindCurrent = "unbind_current"
)

// 只属于第二层，用于计算第一层不解释的挑战关联摘要。
type VerificationContext struct {
	Purpose          string
	Actor            SessionRef
	ExpectedRevision uint64
	Target           *LoginRef // 仅解绑时有目标；注册与登录的 Actor 为空。
}

type RegistrationPolicy interface {
	AuthorizeRegistration(
		context.Context, credential.Principal, []byte,
	) error
}

type RegistrationRequest struct {
	First          credential.Credential
	AdmissionProof []byte
}

type IdentityChange struct {
	Actor            SessionRef
	Reauth           credential.Credential
	ExpectedRevision uint64
}

type AuthPayload struct {
	BackendID     string
	Audience      string
	Session       SessionRef
	IssuedAtUnix  int64
	ExpiresAtUnix int64
}

type LoginTicket struct {
	Payload []byte
	Token   string
}

type LoginResult struct {
	AccountID int64
	Ticket    LoginTicket
}

type TokenIssuer interface {
	IssueLoginToken(context.Context, AuthPayload) (LoginTicket, error)
}

type StatusWrite struct {
	AccountID     int64
	ExpectedEpoch uint64
	Status        AccountStatus
}
