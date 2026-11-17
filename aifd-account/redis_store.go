package account

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"github.com/redis/go-redis/v9"
)

// RedisStoreOptions configures an isolated account namespace, without hash tags.
type RedisStoreOptions struct {
	Space         string
	MaxIdentities int
	Now           func() time.Time
}

// RedisStore owns synchronous account/index/session writes. Each CAS watches one
// key; success confirms the required steps, not a multi-key transaction. The host
// owns the client and supplies primary reads, no account/index eviction, and its
// required persistence/replication. Custom client wrappers must preserve NoRetry.
type RedisStore struct {
	client        redis.UniversalClient
	prefix        string
	maxIdentities int
	now           func() time.Time
}

// NewRedisStore validates configuration without connecting or changing the client.
// Writes disable ambiguous retries individually, preserving other client uses.
func NewRedisStore(client redis.UniversalClient, opts RedisStoreOptions) (*RedisStore, error) {
	if client == nil {
		return nil, ErrStoreMissing
	}
	if !credential.ValidID(opts.Space) || opts.MaxIdentities < 0 || opts.MaxIdentities > maxStoredIdentities {
		return nil, fmt.Errorf("invalid account redis configuration")
	}
	if c, ok := client.(interface{ Options() *redis.ClusterOptions }); ok {
		o := c.Options()
		if o.ReadOnly || o.RouteByLatency || o.RouteRandomly {
			return nil, fmt.Errorf("account redis requires primary-node reads")
		}
	}
	if c, ok := client.(interface{ Options() *redis.Options }); ok {
		o := c.Options()
		if o.ClientSideCacheConfig != nil || o.ClientSideCache != nil {
			return nil, fmt.Errorf("account redis requires uncached reads")
		}
	}
	if opts.MaxIdentities == 0 {
		opts.MaxIdentities = defaultMaxIdentities
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &RedisStore{client: client, prefix: "account:" + opts.Space, maxIdentities: opts.MaxIdentities, now: opts.Now}, nil
}

// CreateAccount writes a private candidate before competing for the unique index.
// Unknown writes retain the candidate; a competing registration is never success.
func (s *RedisStore) CreateAccount(ctx context.Context, ref LoginRef) (AccountSnapshot, error) {
	if err := validateLoginRef(ref); err != nil {
		return AccountSnapshot{}, err
	}
	id, err := s.writeOnce(ctx, "incr", s.prefix+":seq:act").Int64()
	if err != nil {
		return AccountSnapshot{}, unknownWrite("allocate account", err)
	}
	if id <= 0 {
		return AccountSnapshot{}, ErrCorruptState
	}
	a := AccountSnapshot{AccountID: id, Status: AccountStatusActive, Identities: []LoginRef{ref}, IdentityRevision: 1, SessionEpoch: 1}
	raw, err := marshalAccount(a)
	if err != nil {
		return AccountSnapshot{}, err
	}
	ok, err := s.createOnce(ctx, s.accountKey(id), raw, 0)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if !ok {
		return AccountSnapshot{}, ErrCorruptState
	}
	ok, err = s.createOnce(ctx, s.loginKey(ref), formatAccountID(id), 0)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if !ok {
		// This newly allocated ID was never published and is never reused.
		if err := s.deleteOnce(ctx, s.accountKey(id)); err != nil {
			return AccountSnapshot{}, errors.Join(ErrIdentityInUse, err)
		}
		return AccountSnapshot{}, ErrIdentityInUse
	}
	return a, nil
}

// GetAccount reads only the primary record. An unrelated pending index does not
// invalidate other identities or existing sessions on the account.
func (s *RedisStore) GetAccount(ctx context.Context, id int64) (AccountSnapshot, error) {
	if id <= 0 {
		return AccountSnapshot{}, ErrAccountNotFound
	}
	raw, err := s.client.Get(ctx, s.accountKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return AccountSnapshot{}, ErrAccountNotFound
	}
	if err != nil {
		return AccountSnapshot{}, unavailableRead("read account", err)
	}
	return unmarshalAccount(raw, id)
}

// ResolveAccount requires both the index and membership in its account record.
func (s *RedisStore) ResolveAccount(ctx context.Context, ref LoginRef) (AccountSnapshot, error) {
	if err := validateLoginRef(ref); err != nil {
		return AccountSnapshot{}, err
	}
	id, err := s.loadLoginAccountID(ctx, ref)
	if err != nil {
		return AccountSnapshot{}, err
	}
	a, err := s.GetAccount(ctx, id)
	if errors.Is(err, ErrAccountNotFound) {
		return AccountSnapshot{}, ErrCorruptState
	}
	if err != nil {
		return AccountSnapshot{}, err
	}
	if identityIndex(a, ref) < 0 {
		return AccountSnapshot{}, ErrUnavailable
	}
	return a, nil
}

// OpenSession captures the epoch, creates an immutable session, then rechecks
// membership and validity. It never upgrades an old captured epoch.
func (s *RedisStore) OpenSession(ctx context.Context, req OpenSessionRequest) (LoginSession, error) {
	expiry := req.ExpiresAt.Truncate(time.Second)
	if validateLoginRef(req.Identity) != nil || !validSessionID(req.SessionID) || !s.now().Before(expiry) {
		return LoginSession{}, ErrInvalidCredential
	}
	a, err := s.ResolveAccount(ctx, req.Identity)
	if err != nil {
		return LoginSession{}, err
	}
	if a.Status != AccountStatusActive {
		return LoginSession{}, ErrAccountDisabled
	}
	session := LoginSession{Ref: SessionRef{AccountID: a.AccountID, SessionID: req.SessionID}, SessionEpoch: a.SessionEpoch, ExpiresAt: expiry}
	raw, err := marshalSession(session)
	if err != nil {
		return LoginSession{}, err
	}
	ttl := expiry.Sub(s.now())
	if ttl <= 0 {
		return LoginSession{}, ErrSessionInvalid
	}
	ok, err := s.createOnce(ctx, s.sessionKey(req.SessionID), raw, ttl)
	if err != nil {
		return LoginSession{}, err
	}
	if !ok {
		return LoginSession{}, ErrSessionInvalid
	}
	current, err := s.ResolveAccount(ctx, req.Identity)
	if err == nil && current.AccountID != a.AccountID {
		err = ErrSessionInvalid
	}
	if err == nil {
		_, err = s.ValidateSession(ctx, session.Ref)
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		return LoginSession{}, errors.Join(err, s.deleteOnce(cleanup, s.sessionKey(req.SessionID)))
	}
	return session, nil
}

// BindIdentity reserves the index before changing the primary record. A partial
// reservation cannot be adopted by another call, even for the same account.
func (s *RedisStore) BindIdentity(ctx context.Context, req IdentityWrite) (uint64, error) {
	a, actor, err := s.validateIdentityWrite(ctx, req)
	if err != nil {
		return 0, err
	}
	owner, err := s.loadLoginAccountID(ctx, req.Target)
	if err == nil {
		if owner != a.AccountID {
			return 0, ErrIdentityInUse
		}
		if identityIndex(a, req.Target) < 0 {
			return 0, ErrUnavailable
		}
		return a.IdentityRevision, nil
	}
	if !errors.Is(err, ErrAccountNotFound) {
		return 0, err
	}
	if identityIndex(a, req.Target) >= 0 {
		return 0, ErrUnavailable
	}
	if len(a.Identities) >= s.maxIdentities {
		return 0, ErrIdentityLimit
	}
	ok, err := s.createOnce(ctx, s.loginKey(req.Target), formatAccountID(a.AccountID), 0)
	if err != nil {
		return 0, err
	}
	if !ok {
		owner, readErr := s.loadLoginAccountID(ctx, req.Target)
		if readErr != nil || owner == a.AccountID {
			return 0, errors.Join(ErrUnavailable, readErr)
		}
		return 0, ErrIdentityInUse
	}
	updated, err := s.updateAccount(ctx, a.AccountID, func(current *AccountSnapshot) (bool, error) {
		if err := validateIdentityActor(*current, actor, req, s.now()); err != nil {
			return false, err
		}
		if identityIndex(*current, req.Target) >= 0 {
			return false, ErrIdentityRevisionConflict
		}
		if len(current.Identities) >= s.maxIdentities {
			return false, ErrIdentityLimit
		}
		if current.IdentityRevision == math.MaxUint64 {
			return false, ErrIdentityRevisionConflict
		}
		current.Identities = append(current.Identities, req.Target)
		current.IdentityRevision++
		return true, nil
	})
	if err != nil {
		if !errors.Is(err, ErrResultUnknown) {
			// Release only this invocation's reservation after a definite non-commit.
			err = errors.Join(err, s.deleteIndexOnce(ctx, req.Target, a.AccountID))
		}
		return 0, err
	}
	return updated.IdentityRevision, nil
}

// UnbindIdentity removes membership before releasing the index. Sessions remain
// valid; failures after the CAS do not roll back the membership change.
func (s *RedisStore) UnbindIdentity(ctx context.Context, req IdentityWrite) (uint64, error) {
	a, actor, err := s.validateIdentityWrite(ctx, req)
	if err != nil {
		return 0, err
	}
	updated, err := s.updateAccount(ctx, a.AccountID, func(current *AccountSnapshot) (bool, error) {
		if err := validateIdentityActor(*current, actor, req, s.now()); err != nil {
			return false, err
		}
		idx := identityIndex(*current, req.Target)
		if idx < 0 {
			return false, ErrIdentityNotBound
		}
		if len(current.Identities) == 1 {
			return false, ErrLastLoginIdentity
		}
		if current.IdentityRevision == math.MaxUint64 {
			return false, ErrIdentityRevisionConflict
		}
		current.Identities = append(current.Identities[:idx], current.Identities[idx+1:]...)
		current.IdentityRevision++
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if err := s.deleteIndexOnce(ctx, req.Target, a.AccountID); err != nil {
		return 0, err
	}
	return updated.IdentityRevision, nil
}

// ValidateSession reads session then account from the authority, without a
// cross-key snapshot or a dependency on unrelated login indexes.
func (s *RedisStore) ValidateSession(ctx context.Context, ref SessionRef) (LoginSession, error) {
	session, err := s.loadSession(ctx, ref)
	if err != nil {
		return LoginSession{}, err
	}
	a, err := s.GetAccount(ctx, ref.AccountID)
	if errors.Is(err, ErrAccountNotFound) {
		return LoginSession{}, ErrCorruptState
	}
	if err != nil {
		return LoginSession{}, err
	}
	if err := validateStoredSession(a, session, ref, s.now()); err != nil {
		return LoginSession{}, err
	}
	return session, nil
}

// RevokeSession verifies ownership before deleting an immutable, nonreusable ID.
func (s *RedisStore) RevokeSession(ctx context.Context, ref SessionRef) error {
	if !validSessionRef(ref) {
		return ErrSessionInvalid
	}
	_, err := s.loadSession(ctx, ref)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.deleteOnce(ctx, s.sessionKey(ref.SessionID))
}

// AdvanceSessionEpoch invalidates at most the caller's expected epoch.
func (s *RedisStore) AdvanceSessionEpoch(ctx context.Context, id int64, expected uint64) (uint64, error) {
	a, err := s.updateAccount(ctx, id, func(a *AccountSnapshot) (bool, error) {
		if expected == 0 || expected > a.SessionEpoch {
			return false, ErrEpochConflict
		}
		if expected < a.SessionEpoch {
			return false, nil
		}
		next, err := nextVersion(a.SessionEpoch)
		if err != nil {
			return false, err
		}
		a.SessionEpoch = next
		return true, nil
	})
	return a.SessionEpoch, err
}

// ChangeAccountStatus atomically changes status and epoch on the primary key.
func (s *RedisStore) ChangeAccountStatus(ctx context.Context, req StatusWrite) (AccountSnapshot, error) {
	if req.Status != AccountStatusActive && req.Status != AccountStatusDisabled {
		return AccountSnapshot{}, ErrInvalidCredential
	}
	return s.updateAccount(ctx, req.AccountID, func(a *AccountSnapshot) (bool, error) {
		if req.ExpectedEpoch == 0 || req.ExpectedEpoch != a.SessionEpoch {
			return false, ErrEpochConflict
		}
		if req.Status == a.Status {
			return false, nil
		}
		next, err := nextVersion(a.SessionEpoch)
		if err != nil {
			return false, err
		}
		a.Status, a.SessionEpoch = req.Status, next
		return true, nil
	})
}

func (s *RedisStore) validateIdentityWrite(ctx context.Context, req IdentityWrite) (AccountSnapshot, LoginSession, error) {
	if validateLoginRef(req.Target) != nil || validateLoginRef(req.Reauthenticated) != nil {
		return AccountSnapshot{}, LoginSession{}, ErrInvalidCredential
	}
	actor, err := s.ValidateSession(ctx, req.Actor)
	if err != nil {
		return AccountSnapshot{}, LoginSession{}, err
	}
	a, err := s.GetAccount(ctx, req.Actor.AccountID)
	if err != nil {
		return AccountSnapshot{}, LoginSession{}, err
	}
	if err := validateIdentityActor(a, actor, req, s.now()); err != nil {
		return AccountSnapshot{}, LoginSession{}, err
	}
	owner, err := s.loadLoginAccountID(ctx, req.Reauthenticated)
	if err != nil {
		return AccountSnapshot{}, LoginSession{}, err
	}
	if owner != a.AccountID {
		return AccountSnapshot{}, LoginSession{}, ErrInvalidCredential
	}
	return a, actor, nil
}

func validateIdentityActor(a AccountSnapshot, actor LoginSession, req IdentityWrite, now time.Time) error {
	if err := validateStoredSession(a, actor, req.Actor, now); err != nil {
		return err
	}
	if identityIndex(a, req.Reauthenticated) < 0 {
		return ErrInvalidCredential
	}
	if req.ExpectedRevision == 0 || a.IdentityRevision != req.ExpectedRevision {
		return ErrIdentityRevisionConflict
	}
	return nil
}

func (s *RedisStore) loginKey(ref LoginRef) string {
	return s.prefix + ":login:" + ref.AuthorityID + ":" + hex.EncodeToString(ref.SubjectDigest[:])
}
func (s *RedisStore) accountKey(id int64) string  { return s.prefix + ":act:" + formatAccountID(id) }
func (s *RedisStore) sessionKey(id string) string { return s.prefix + ":sess:" + id }

var _ Store = (*RedisStore)(nil)
