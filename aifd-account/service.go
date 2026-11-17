package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"strconv"
	"time"
)

type ServiceAPI interface {
	BuildVerificationBinding(
		VerificationContext,
	) (credential.ChallengeBinding, error)

	Register(ctx context.Context, req RegistrationRequest) (AccountSnapshot, error)
	GetAccount(ctx context.Context, accountID int64) (AccountSnapshot, error)
	Login(ctx context.Context, proof credential.Credential) (LoginResult, error)

	BindIdentity(
		ctx context.Context, change IdentityChange, candidate credential.Credential,
	) (uint64, error)
	UnbindIdentity(
		ctx context.Context, change IdentityChange, target LoginRef,
	) (uint64, error)

	ValidateSession(ctx context.Context, ref SessionRef) (LoginSession, error)
	IssueLoginTicket(ctx context.Context, ref SessionRef) (LoginTicket, error)
	RevokeSession(ctx context.Context, ref SessionRef) error
	RevokeAllSessions(
		ctx context.Context, accountID int64, expectedEpoch uint64,
	) (uint64, error)
	ChangeAccountStatus(ctx context.Context, req StatusWrite) (AccountSnapshot, error)
}

type Options struct {
	BackendID          string
	Audience           string
	IndexKey           []byte
	SessionTTL         time.Duration
	TicketTTL          time.Duration
	RegistrationPolicy RegistrationPolicy
	Now                func() time.Time
}

// Service owns verified account actions; the host owns route authorization and business admission.
type Service struct {
	store    Store
	verifier credential.Verifier
	issuer   TokenIssuer
	opts     Options
	now      func() time.Time
}

// NewService validates and copies configuration without opening resources or registering routes.
func NewService(store Store, verifier credential.Verifier, issuer TokenIssuer, opts Options) (*Service, error) {
	if store == nil {
		return nil, ErrStoreMissing
	}
	if verifier == nil {
		return nil, ErrInvalidCredential
	}
	if issuer == nil {
		return nil, ErrTokenIssuerMissing
	}
	if !credential.ValidID(opts.BackendID) || len(opts.Audience) == 0 || len(opts.Audience) > 256 || len(opts.IndexKey) < 32 {
		return nil, fmt.Errorf("invalid account configuration")
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 24 * time.Hour
	}
	if opts.TicketTTL == 0 {
		opts.TicketTTL = 30 * time.Minute
	}
	if opts.SessionTTL < time.Second || opts.TicketTTL < time.Second || opts.TicketTTL > opts.SessionTTL {
		return nil, fmt.Errorf("invalid account expiration configuration")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.IndexKey = append([]byte(nil), opts.IndexKey...)
	return &Service{store: store, verifier: verifier, issuer: issuer, opts: opts, now: opts.Now}, nil
}

// BuildVerificationBinding binds the exact action context; the digest alone grants no authority.
func (s *Service) BuildVerificationBinding(v VerificationContext) (credential.ChallengeBinding, error) {
	switch v.Purpose {
	case VerifyRegister, VerifyLogin:
		if v.Actor != (SessionRef{}) || v.ExpectedRevision != 0 || v.Target != nil {
			return credential.ChallengeBinding{}, ErrInvalidCredential
		}
	case VerifyBindCandidate, VerifyBindCurrent, VerifyUnbindCurrent:
		if !validSessionRef(v.Actor) || v.ExpectedRevision == 0 {
			return credential.ChallengeBinding{}, ErrInvalidCredential
		}
		if (v.Purpose == VerifyUnbindCurrent) != (v.Target != nil) {
			return credential.ChallengeBinding{}, ErrInvalidCredential
		}
	default:
		return credential.ChallengeBinding{}, ErrInvalidCredential
	}
	authority, digest := "", ""
	if v.Target != nil {
		if err := validateLoginRef(*v.Target); err != nil {
			return credential.ChallengeBinding{}, err
		}
		authority = v.Target.AuthorityID
		digest = hex.EncodeToString(v.Target.SubjectDigest[:])
	}
	encoded := encodeFields("aifd-verification-v1", s.opts.BackendID, v.Purpose, formatAccountID(v.Actor.AccountID), v.Actor.SessionID, strconv.FormatUint(v.ExpectedRevision, 10), authority, digest)
	return credential.ChallengeBinding{Purpose: v.Purpose, ScopeDigest: sha256.Sum256(encoded)}, nil
}

// Register verifies a first identity and explicit policy before publishing an account, without login.
func (s *Service) Register(ctx context.Context, req RegistrationRequest) (AccountSnapshot, error) {
	if len(req.AdmissionProof) > 16<<10 {
		return AccountSnapshot{}, ErrRegistrationDenied
	}
	principal, err := s.verify(ctx, req.First, VerificationContext{Purpose: VerifyRegister})
	if err != nil {
		return AccountSnapshot{}, err
	}
	if s.opts.RegistrationPolicy == nil {
		return AccountSnapshot{}, ErrRegistrationDenied
	}
	if err := s.opts.RegistrationPolicy.AuthorizeRegistration(ctx, principal, req.AdmissionProof); err != nil {
		return AccountSnapshot{}, fmt.Errorf("%w: %w", ErrRegistrationDenied, err)
	}
	ref, err := loginRefFromPrincipal(s.opts.IndexKey, principal)
	if err != nil {
		return AccountSnapshot{}, err
	}
	return s.store.CreateAccount(ctx, ref)
}

// GetAccount reads account state. The host must authorize the requested account ID.
func (s *Service) GetAccount(ctx context.Context, id int64) (AccountSnapshot, error) {
	return s.store.GetAccount(ctx, id)
}

// Login only opens a session for an existing verified identity; it never creates accounts.
func (s *Service) Login(ctx context.Context, proof credential.Credential) (LoginResult, error) {
	principal, err := s.verify(ctx, proof, VerificationContext{Purpose: VerifyLogin})
	if err != nil {
		return LoginResult{}, err
	}
	ref, err := loginRefFromPrincipal(s.opts.IndexKey, principal)
	if err != nil {
		return LoginResult{}, err
	}
	id, err := newSessionID()
	if err != nil {
		return LoginResult{}, err
	}
	session, err := s.store.OpenSession(ctx, OpenSessionRequest{Identity: ref, SessionID: id, ExpiresAt: s.now().Add(s.opts.SessionTTL).Truncate(time.Second)})
	if err != nil {
		return LoginResult{}, err
	}
	ticket, err := s.IssueLoginTicket(ctx, session.Ref)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = s.store.RevokeSession(cleanup, session.Ref)
		return LoginResult{}, err
	}
	return LoginResult{AccountID: session.Ref.AccountID, Ticket: ticket}, nil
}

// BindIdentity verifies the candidate first, then reauthenticates the current actor for this revision.
func (s *Service) BindIdentity(ctx context.Context, change IdentityChange, candidate credential.Credential) (uint64, error) {
	v := VerificationContext{Purpose: VerifyBindCandidate, Actor: change.Actor, ExpectedRevision: change.ExpectedRevision}
	principal, err := s.verify(ctx, candidate, v)
	if err != nil {
		return 0, err
	}
	target, err := loginRefFromPrincipal(s.opts.IndexKey, principal)
	if err != nil {
		return 0, err
	}
	v.Purpose = VerifyBindCurrent
	current, err := s.verify(ctx, change.Reauth, v)
	if err != nil {
		return 0, err
	}
	reauth, err := loginRefFromPrincipal(s.opts.IndexKey, current)
	if err != nil {
		return 0, err
	}
	return s.store.BindIdentity(ctx, IdentityWrite{Actor: change.Actor, Reauthenticated: reauth, Target: target, ExpectedRevision: change.ExpectedRevision})
}

// UnbindIdentity reauthenticates the actor for this exact target and leaves all sessions unchanged.
func (s *Service) UnbindIdentity(ctx context.Context, change IdentityChange, target LoginRef) (uint64, error) {
	principal, err := s.verify(ctx, change.Reauth, VerificationContext{Purpose: VerifyUnbindCurrent, Actor: change.Actor, ExpectedRevision: change.ExpectedRevision, Target: &target})
	if err != nil {
		return 0, err
	}
	ref, err := loginRefFromPrincipal(s.opts.IndexKey, principal)
	if err != nil {
		return 0, err
	}
	return s.store.UnbindIdentity(ctx, IdentityWrite{Actor: change.Actor, Reauthenticated: ref, Target: target, ExpectedRevision: change.ExpectedRevision})
}

// ValidateSession consults the authoritative store; a SessionRef is not an authentication proof.
func (s *Service) ValidateSession(ctx context.Context, ref SessionRef) (LoginSession, error) {
	return s.store.ValidateSession(ctx, ref)
}

// IssueLoginTicket requires a host-authenticated caller and never extends the session deadline.
func (s *Service) IssueLoginTicket(ctx context.Context, ref SessionRef) (LoginTicket, error) {
	session, err := s.store.ValidateSession(ctx, ref)
	if err != nil {
		return LoginTicket{}, err
	}
	now := s.now().Truncate(time.Second)
	expiry := now.Add(s.opts.TicketTTL).Truncate(time.Second)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	if !now.Before(expiry) {
		return LoginTicket{}, ErrSessionInvalid
	}
	ticket, err := s.issuer.IssueLoginToken(ctx, AuthPayload{BackendID: s.opts.BackendID, Audience: s.opts.Audience, Session: ref, IssuedAtUnix: now.Unix(), ExpiresAtUnix: expiry.Unix()})
	if err != nil {
		return LoginTicket{}, err
	}
	if len(ticket.Payload) == 0 || ticket.Token == "" {
		return LoginTicket{}, ErrTokenIssuerMissing
	}
	return LoginTicket{Payload: append([]byte(nil), ticket.Payload...), Token: ticket.Token}, nil
}

// RevokeSession revokes a host-authorized target; absence is idempotent success.
func (s *Service) RevokeSession(ctx context.Context, ref SessionRef) error {
	return s.store.RevokeSession(ctx, ref)
}

// RevokeAllSessions advances only the requested epoch, so response-loss retries preserve new sessions.
func (s *Service) RevokeAllSessions(ctx context.Context, id int64, expected uint64) (uint64, error) {
	return s.store.AdvanceSessionEpoch(ctx, id, expected)
}

// ChangeAccountStatus is a trusted administrative API using strict epoch comparison.
func (s *Service) ChangeAccountStatus(ctx context.Context, req StatusWrite) (AccountSnapshot, error) {
	return s.store.ChangeAccountStatus(ctx, req)
}

func (s *Service) verify(ctx context.Context, proof credential.Credential, v VerificationContext) (credential.Principal, error) {
	if !credential.ValidID(proof.ConnectorID) || len(proof.Proof) == 0 || len(proof.Proof) > credential.MaxProofBytes {
		return credential.Principal{}, ErrInvalidCredential
	}
	binding, err := s.BuildVerificationBinding(v)
	if err != nil {
		return credential.Principal{}, err
	}
	principal, err := s.verifier.Verify(ctx, credential.VerifyRequest{Credential: proof, Binding: binding})
	if err != nil {
		return credential.Principal{}, err
	}
	if !credential.ValidID(principal.AuthorityID) || principal.Subject == "" || len(principal.Subject) > credential.MaxProofBytes {
		return credential.Principal{}, ErrInvalidCredential
	}
	return principal, nil
}

func newSessionID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
