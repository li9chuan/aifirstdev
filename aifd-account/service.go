package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const defaultTokenTTL = 30 * time.Minute

// Options configure account service behavior.
type Options struct {
	TokenTTL time.Duration
	Now      func() time.Time
}

// Service resolves provider credentials into accounts and signed login tokens.
type Service struct {
	store    Store
	verifier Verifier
	issuer   TokenIssuer
	opts     Options
	now      func() time.Time
}

// NewService creates an account center service.
func NewService(store Store, verifier Verifier, issuer TokenIssuer, opts Options) (*Service, error) {
	if store == nil {
		return nil, ErrStoreMissing
	}
	if verifier == nil {
		return nil, fmt.Errorf("%w: verifier is nil", ErrInvalidCredential)
	}
	if issuer == nil {
		return nil, ErrTokenIssuerMissing
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if opts.TokenTTL == 0 {
		opts.TokenTTL = defaultTokenTTL
	}
	return &Service{store: store, verifier: verifier, issuer: issuer, opts: opts, now: now}, nil
}

// Login verifies credentials, resolves the account, and returns a signed login token.
func (s *Service) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	if s == nil {
		return LoginResult{}, ErrStoreMissing
	}
	if ctx == nil {
		ctx = context.Background()
	}
	principal, err := s.verifier.Verify(ctx, req.Credential)
	if err != nil {
		return LoginResult{}, err
	}
	loginRef, err := LoginRefFromPrincipal(principal)
	if err != nil {
		return LoginResult{}, err
	}
	resolved, err := s.store.ResolveOrCreate(ctx, loginRef, req.AutoCreate)
	if err != nil {
		return LoginResult{}, err
	}
	ttl := req.TTL
	if ttl == 0 {
		ttl = s.opts.TokenTTL
	}
	now := s.now()
	expiresAt := now.Add(ttl)
	tokenID := req.TokenID
	if tokenID == "" {
		var err error
		tokenID, err = newTokenID()
		if err != nil {
			return LoginResult{}, err
		}
	}
	payload := AuthPayload{
		AccountID:    resolved.AccountID,
		Provider:     loginRef.Provider,
		SubjectHash:  loginRef.SubjectHash,
		TokenID:      tokenID,
		IssuedAt:     now.Unix(),
		ExpiresAt:    expiresAt.Unix(),
		IsNewAccount: resolved.IsNewAccount,
	}
	token, err := s.issuer.IssueLoginToken(ctx, payload)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		AccountID:    resolved.AccountID,
		Provider:     loginRef.Provider,
		SubjectHash:  loginRef.SubjectHash,
		TokenID:      tokenID,
		IssuedAt:     now,
		ExpiresAt:    expiresAt,
		IsNewAccount: resolved.IsNewAccount,
		Payload:      payload,
		Token:        token,
	}, nil
}

// GetAccount returns one account record without changing login or account state.
func (s *Service) GetAccount(ctx context.Context, accountID int64) (AccountRecord, error) {
	if s == nil || s.store == nil {
		return AccountRecord{}, ErrStoreMissing
	}
	reader, ok := s.store.(AccountReader)
	if !ok {
		return AccountRecord{}, ErrStoreMissing
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return reader.GetAccount(ctx, accountID)
}

func newTokenID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
