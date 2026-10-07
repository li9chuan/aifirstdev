package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	account "github.com/li9chuan/aifirstdev/aifd-account"
	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
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
	issuer := account.TokenIssuerFunc(func(_ context.Context, payload account.AuthPayload) (string, error) {
		payloadBytes, err := auth.CanonicalPayloadBytes(authPayloadPB(payload))
		if err != nil {
			return "", err
		}
		return signer.Sign(payloadBytes, token.SignOptions{
			IssuedAt:  payload.IssuedAt,
			ExpiresAt: payload.ExpiresAt,
		})
	})
	service, err := account.NewService(
		store,
		account.NewDemoVerifier(),
		issuer,
		account.Options{
			TokenTTL: 30 * time.Minute,
		},
	)
	if err != nil {
		return nil, err
	}
	return &AccountApp{Service: service, Verifier: verifier, RealmID: defaultRealmID}, nil
}

// DemoLogin resolves a demo account and returns a signed proxy login token.
func (a *AccountApp) DemoLogin(ctx context.Context, ownerID int64) (account.LoginResult, error) {
	if a == nil || a.Service == nil {
		return account.LoginResult{}, fmt.Errorf("account app is nil")
	}
	subject := strconv.FormatInt(ownerID, 10)
	return a.Service.Login(ctx, account.LoginRequest{
		Credential: account.Credential{
			Provider: account.ProviderDemo,
			Subject:  subject,
			Secret:   account.DemoSecret(subject),
		},
		AutoCreate: true,
	})
}

// DemoAuth returns the client login protobuf used by proxy and owner auth.
func (a *AccountApp) DemoAuth(ctx context.Context, ownerID int64) (*pbmsg.MceAuth, error) {
	result, err := a.DemoLogin(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return &pbmsg.MceAuth{
		Payload: authPayloadPB(result.Payload),
		Token:   result.Token,
	}, nil
}

func authPayloadPB(payload account.AuthPayload) *pbmsg.MAuthPayload {
	return &pbmsg.MAuthPayload{
		AccountId:    payload.AccountID,
		Uid:          payload.AccountID,
		RealmId:      defaultRealmID,
		Provider:     payload.Provider,
		SubjectHash:  payload.SubjectHash,
		TokenId:      payload.TokenID,
		IssuedAt:     payload.IssuedAt,
		ExpiresAt:    payload.ExpiresAt,
		IsNewAccount: payload.IsNewAccount,
	}
}
