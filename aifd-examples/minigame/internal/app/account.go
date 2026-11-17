package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	account "github.com/li9chuan/aifirstdev/aifd-account"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	"google.golang.org/protobuf/proto"
)

const (
	defaultRealmID      int32 = 1
	minigameTokenKeyID        = "minigame-dev"
	minigameTokenSecret       = "minigame-dev-token-secret"
)

// AccountApp holds the minigame account center used to issue signed login tokens.
type AccountApp struct {
	Service  *account.Service
	Verifier *token.Verifier
	RealmID  int32
}

// NewAccountApp creates an in-memory account center for the minigame example.
func NewAccountApp() (*AccountApp, error) {
	return NewAccountAppWithStore(account.NewMemoryStore(account.MemoryOptions{}))
}

// NewAccountAppWithStore creates a minigame account center with an injected store.
func NewAccountAppWithStore(store account.Store) (*AccountApp, error) {
	key := token.Key{ID: minigameTokenKeyID, Secret: []byte(minigameTokenSecret)}
	signer, err := token.NewSigner(key, token.Options{})
	if err != nil {
		return nil, err
	}
	verifier, err := token.NewVerifier([]token.Key{key}, token.Options{})
	if err != nil {
		return nil, err
	}
	issuer := account.TokenIssuerFunc(func(_ context.Context, payload account.AuthPayload) (account.LoginTicket, error) {
		payloadBytes, err := auth.CanonicalPayloadBytes(authPayloadPB(payload))
		if err != nil {
			return account.LoginTicket{}, err
		}
		signed, err := signer.Sign(payloadBytes, token.SignOptions{
			IssuedAt:  payload.IssuedAtUnix,
			ExpiresAt: payload.ExpiresAtUnix,
		})
		return account.LoginTicket{Payload: payloadBytes, Token: signed}, err
	})
	service, err := account.NewService(
		store,
		account.NewDemoVerifier(),
		issuer,
		account.Options{
			BackendID: auth.AccountBackendID, Audience: auth.AccountAudience,
			IndexKey:  []byte("minigame-development-index-key-32bytes"),
			TicketTTL: 30 * time.Minute, SessionTTL: 24 * time.Hour,
			RegistrationPolicy: account.RegistrationPolicyFunc(func(context.Context, credential.Principal, []byte) error { return nil }),
		},
	)
	if err != nil {
		return nil, err
	}
	return &AccountApp{Service: service, Verifier: verifier, RealmID: defaultRealmID}, nil
}

// DemoLogin logs into an already registered demo account.
func (a *AccountApp) DemoLogin(ctx context.Context, ownerID int64) (account.LoginResult, error) {
	if a == nil || a.Service == nil {
		return account.LoginResult{}, fmt.Errorf("account app is nil")
	}
	subject := strconv.FormatInt(ownerID, 10)
	return a.Service.Login(ctx, account.DemoCredential(subject))
}

// DemoAuth explicitly provisions a development account, then logs in. Real
// applications expose registration and login as separate authorized actions.
func (a *AccountApp) DemoAuth(ctx context.Context, ownerID int64) (*pbmsg.MceAuth, error) {
	if a == nil || a.Service == nil || ownerID <= 0 {
		return nil, fmt.Errorf("invalid demo account")
	}
	if _, err := a.Service.Register(ctx, account.RegistrationRequest{First: account.DemoCredential(strconv.FormatInt(ownerID, 10))}); err != nil && !errors.Is(err, account.ErrIdentityInUse) {
		return nil, err
	}
	result, err := a.DemoLogin(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var payload pbmsg.MAuthPayload
	if err := proto.Unmarshal(result.Ticket.Payload, &payload); err != nil {
		return nil, err
	}
	return &pbmsg.MceAuth{Payload: &payload, Token: result.Ticket.Token}, nil
}

func authPayloadPB(payload account.AuthPayload) *pbmsg.MAuthPayload {
	return &pbmsg.MAuthPayload{
		AccountId: payload.Session.AccountID,
		Uid:       payload.Session.AccountID,
		RealmId:   defaultRealmID,
		BackendId: payload.BackendID, Audience: payload.Audience, SessionId: payload.Session.SessionID,
		IssuedAt: payload.IssuedAtUnix, ExpiresAt: payload.ExpiresAtUnix,
	}
}
