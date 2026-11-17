// Package externalauth shows an existing issuer entering AIFD Proxy/Runtime
// without AIFD accounts, account IDs, login sessions, or ticket formats.
package externalauth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
)

var ErrDenied = errors.New("external authentication denied")

// VerifiedToken is returned only after the original issuer verifies its token.
// SessionID stays empty for a stateless original token.
type VerifiedToken struct {
	Account, SessionID, Tenant string
	Scopes                     []string
	ExpiresAt                  time.Time
}
type TokenVerifier interface {
	Verify(context.Context, string) (VerifiedToken, error)
}
type TokenVerifierFunc func(context.Context, string) (VerifiedToken, error)

func (f TokenVerifierFunc) Verify(ctx context.Context, token string) (VerifiedToken, error) {
	return f(ctx, token)
}

// Adapter is application wiring, not a universal token protocol. Check queries
// the original account/session system; Resolve owns business UID admission.
type Adapter struct {
	BackendID, Tenant, RequiredScope string
	Mode                             proxy.AuthCheckMode
	Tokens                           TokenVerifier
	Check                            func(context.Context, string, string) error
	Resolve                          func(context.Context, string) (int64, error)
}

func (a *Adapter) Authenticate(ctx context.Context, req proxy.AuthRequest) (proxy.Identity, error) {
	if a == nil || a.Tokens == nil || a.Resolve == nil || a.BackendID == "" {
		return proxy.Identity{}, ErrDenied
	}
	if a.Mode != proxy.AuthCheckExpiryOnly && a.Mode != proxy.AuthCheckPeriodic && a.Mode != proxy.AuthCheckPerRequest {
		return proxy.Identity{}, ErrDenied
	}
	token, err := a.Tokens.Verify(ctx, req.Token)
	if err != nil {
		return proxy.Identity{}, ErrDenied
	}
	if token.Account == "" || !time.Now().Before(token.ExpiresAt) || token.Tenant != a.Tenant || !slices.Contains(token.Scopes, a.RequiredScope) {
		return proxy.Identity{}, ErrDenied
	}
	if a.Mode != proxy.AuthCheckExpiryOnly {
		if a.Check == nil || a.Check(ctx, token.Account, token.SessionID) != nil {
			return proxy.Identity{}, ErrDenied
		}
	}
	uid, err := a.Resolve(ctx, token.Account)
	if err != nil || uid <= 0 {
		return proxy.Identity{}, ErrDenied
	}
	identity := proxy.Identity{UID: uid, BackendID: a.BackendID, Account: token.Account, SessionID: token.SessionID, ValidUntil: token.ExpiresAt}
	if err := identity.AuthContext().Validate(time.Now()); err != nil {
		return proxy.Identity{}, ErrDenied
	}
	return identity, nil
}

func (a *Adapter) Revalidate(ctx context.Context, auth proxy.AuthContext) error {
	if a == nil || a.Mode == proxy.AuthCheckExpiryOnly || a.Check == nil || auth.BackendID != a.BackendID || auth.Validate(time.Now()) != nil {
		return ErrDenied
	}
	return a.Check(ctx, auth.Account, auth.SessionID)
}

// Handshake reads an original Bearer token; it does not mint a replacement token.
func (a *Adapter) Handshake(ctx context.Context, req proxy.AuthRequest) (proxy.HandshakeResult, error) {
	for name, value := range req.Header {
		if strings.EqualFold(name, "Authorization") {
			scheme, token, ok := strings.Cut(value, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				return proxy.HandshakeResult{Handled: true}, ErrDenied
			}
			req.Token = token
			break
		}
	}
	identity, err := a.Authenticate(ctx, req)
	return proxy.HandshakeResult{Handled: true, Identity: identity}, err
}
