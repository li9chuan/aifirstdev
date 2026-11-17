package account

import (
	"context"
	"math"
	"sync"
	"time"
)

// MemoryOptions configures the local reference store. It is not durable storage.
type MemoryOptions struct {
	MaxIdentities int
	Now           func() time.Time
}

// MemoryStore owns all account/index/session state under one mutex for local examples and tests.
type MemoryStore struct {
	mu            sync.Mutex
	nextAccountID int64
	accounts      map[int64]AccountSnapshot
	loginAccounts map[LoginRef]int64
	sessions      map[string]LoginSession
	maxIdentities int
	now           func() time.Time
}

// NewMemoryStore creates an empty store. Nonpositive limits use the default of eight.
func NewMemoryStore(opts MemoryOptions) *MemoryStore {
	if opts.MaxIdentities <= 0 {
		opts.MaxIdentities = defaultMaxIdentities
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &MemoryStore{nextAccountID: 1000, accounts: map[int64]AccountSnapshot{}, loginAccounts: map[LoginRef]int64{}, sessions: map[string]LoginSession{}, maxIdentities: opts.MaxIdentities, now: opts.Now}
}

// CreateAccount atomically publishes a new account and its first identity.
func (s *MemoryStore) CreateAccount(ctx context.Context, ref LoginRef) (AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AccountSnapshot{}, err
	}
	if err := validateLoginRef(ref); err != nil {
		return AccountSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loginAccounts[ref]; ok {
		if _, err := s.resolveUnsafe(ref); err != nil {
			return AccountSnapshot{}, err
		}
		return AccountSnapshot{}, ErrIdentityInUse
	}
	if s.nextAccountID == math.MaxInt64 {
		return AccountSnapshot{}, ErrCorruptState
	}
	s.nextAccountID++
	a := AccountSnapshot{AccountID: s.nextAccountID, Status: AccountStatusActive, Identities: []LoginRef{ref}, IdentityRevision: 1, SessionEpoch: 1}
	s.accounts[a.AccountID] = a
	s.loginAccounts[ref] = a.AccountID
	return copySnapshot(a), nil
}

// GetAccount reads the primary account record without requiring ACTIVE status.
func (s *MemoryStore) GetAccount(ctx context.Context, id int64) (AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AccountSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.accountUnsafe(id)
	return copySnapshot(a), err
}

// ResolveAccount reads a registered identity without creating an account.
func (s *MemoryStore) ResolveAccount(ctx context.Context, ref LoginRef) (AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AccountSnapshot{}, err
	}
	if err := validateLoginRef(ref); err != nil {
		return AccountSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.resolveUnsafe(ref)
	return copySnapshot(a), err
}

// OpenSession atomically checks ownership and ACTIVE status and captures the current epoch.
func (s *MemoryStore) OpenSession(ctx context.Context, req OpenSessionRequest) (LoginSession, error) {
	if err := ctx.Err(); err != nil {
		return LoginSession{}, err
	}
	req.ExpiresAt = req.ExpiresAt.Truncate(time.Second)
	if validateLoginRef(req.Identity) != nil || !validSessionID(req.SessionID) || !s.now().Before(req.ExpiresAt) {
		return LoginSession{}, ErrInvalidCredential
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.resolveUnsafe(req.Identity)
	if err != nil {
		return LoginSession{}, err
	}
	if a.Status != AccountStatusActive {
		return LoginSession{}, ErrAccountDisabled
	}
	if _, ok := s.sessions[req.SessionID]; ok {
		return LoginSession{}, ErrSessionInvalid
	}
	session := LoginSession{Ref: SessionRef{AccountID: a.AccountID, SessionID: req.SessionID}, SessionEpoch: a.SessionEpoch, ExpiresAt: req.ExpiresAt}
	s.sessions[req.SessionID] = session
	return session, nil
}

// BindIdentity changes identities after actor, reauthentication and revision checks.
func (s *MemoryStore) BindIdentity(ctx context.Context, req IdentityWrite) (uint64, error) {
	return s.changeIdentity(ctx, req, false)
}

// UnbindIdentity preserves existing sessions and rejects removing the last identity.
func (s *MemoryStore) UnbindIdentity(ctx context.Context, req IdentityWrite) (uint64, error) {
	return s.changeIdentity(ctx, req, true)
}

func (s *MemoryStore) changeIdentity(ctx context.Context, req IdentityWrite, remove bool) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if validateLoginRef(req.Target) != nil || validateLoginRef(req.Reauthenticated) != nil {
		return 0, ErrInvalidCredential
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.sessionUnsafe(req.Actor); err != nil {
		return 0, err
	}
	a, err := s.accountUnsafe(req.Actor.AccountID)
	if err != nil {
		return 0, err
	}
	if s.loginAccounts[req.Reauthenticated] != a.AccountID || identityIndex(a, req.Reauthenticated) < 0 {
		return 0, ErrInvalidCredential
	}
	if req.ExpectedRevision == 0 || req.ExpectedRevision != a.IdentityRevision {
		return 0, ErrIdentityRevisionConflict
	}
	idx := identityIndex(a, req.Target)
	owner, exists := s.loginAccounts[req.Target]
	if exists && (owner == a.AccountID) != (idx >= 0) {
		return 0, ErrCorruptState
	}
	if remove {
		if idx < 0 {
			return 0, ErrIdentityNotBound
		}
		if len(a.Identities) == 1 {
			return 0, ErrLastLoginIdentity
		}
	} else {
		if exists && owner != a.AccountID {
			return 0, ErrIdentityInUse
		}
		if idx >= 0 {
			return a.IdentityRevision, nil
		}
		if len(a.Identities) >= s.maxIdentities {
			return 0, ErrIdentityLimit
		}
	}
	if a.IdentityRevision == math.MaxUint64 {
		return 0, ErrIdentityRevisionConflict
	}
	a = copySnapshot(a)
	a.IdentityRevision++
	if remove {
		a.Identities = append(a.Identities[:idx], a.Identities[idx+1:]...)
		delete(s.loginAccounts, req.Target)
	} else {
		a.Identities = append(a.Identities, req.Target)
		s.loginAccounts[req.Target] = a.AccountID
	}
	s.accounts[a.AccountID] = a
	return a.IdentityRevision, nil
}

// ValidateSession rejects missing, expired, revoked and incorrectly owned sessions.
func (s *MemoryStore) ValidateSession(ctx context.Context, ref SessionRef) (LoginSession, error) {
	if err := ctx.Err(); err != nil {
		return LoginSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionUnsafe(ref)
}

// RevokeSession deletes only the named account's session; absence is idempotent success.
func (s *MemoryStore) RevokeSession(ctx context.Context, ref SessionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validSessionRef(ref) {
		return ErrSessionInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[ref.SessionID]
	if !ok {
		return nil
	}
	if session.Ref != ref {
		return ErrSessionInvalid
	}
	delete(s.sessions, ref.SessionID)
	return nil
}

// AdvanceSessionEpoch invalidates the requested epoch once, preserving later logins on retries.
func (s *MemoryStore) AdvanceSessionEpoch(ctx context.Context, id int64, expected uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.accountUnsafe(id)
	if err != nil {
		return 0, err
	}
	if expected == 0 || expected > a.SessionEpoch {
		return 0, ErrEpochConflict
	}
	if expected < a.SessionEpoch {
		return a.SessionEpoch, nil
	}
	a.SessionEpoch, err = nextVersion(a.SessionEpoch)
	if err != nil {
		return 0, err
	}
	s.accounts[id] = a
	return a.SessionEpoch, nil
}

// ChangeAccountStatus uses strict CAS; every actual transition advances epoch.
func (s *MemoryStore) ChangeAccountStatus(ctx context.Context, req StatusWrite) (AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AccountSnapshot{}, err
	}
	if req.Status != AccountStatusActive && req.Status != AccountStatusDisabled {
		return AccountSnapshot{}, ErrInvalidCredential
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.accountUnsafe(req.AccountID)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if req.ExpectedEpoch == 0 || req.ExpectedEpoch != a.SessionEpoch {
		return AccountSnapshot{}, ErrEpochConflict
	}
	if req.Status != a.Status {
		a.SessionEpoch, err = nextVersion(a.SessionEpoch)
		if err != nil {
			return AccountSnapshot{}, err
		}
		a.Status = req.Status
		s.accounts[a.AccountID] = a
	}
	return copySnapshot(a), nil
}

// accountUnsafe requires mu. Unrelated pending indexes do not invalidate sessions.
func (s *MemoryStore) accountUnsafe(id int64) (AccountSnapshot, error) {
	a, ok := s.accounts[id]
	if !ok {
		return AccountSnapshot{}, ErrAccountNotFound
	}
	if a.AccountID != id || validateSnapshot(a) != nil {
		return AccountSnapshot{}, ErrCorruptState
	}
	return a, nil
}

// resolveUnsafe requires mu.
func (s *MemoryStore) resolveUnsafe(ref LoginRef) (AccountSnapshot, error) {
	id, ok := s.loginAccounts[ref]
	if !ok {
		return AccountSnapshot{}, ErrAccountNotFound
	}
	a, err := s.accountUnsafe(id)
	if err != nil {
		return AccountSnapshot{}, ErrCorruptState
	}
	if identityIndex(a, ref) < 0 {
		return AccountSnapshot{}, ErrUnavailable
	}
	return a, nil
}

// sessionUnsafe requires mu; session identity is independent from the current identity set.
func (s *MemoryStore) sessionUnsafe(ref SessionRef) (LoginSession, error) {
	if !validSessionRef(ref) {
		return LoginSession{}, ErrSessionInvalid
	}
	session, ok := s.sessions[ref.SessionID]
	if !ok || session.Ref != ref {
		return LoginSession{}, ErrSessionInvalid
	}
	a, err := s.accountUnsafe(ref.AccountID)
	if err != nil {
		return LoginSession{}, ErrCorruptState
	}
	if err := validateStoredSession(a, session, ref, s.now()); err != nil {
		return LoginSession{}, err
	}
	return session, nil
}
