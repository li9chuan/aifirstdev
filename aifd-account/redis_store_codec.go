package account

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	maxAccountRecordBytes = 1 << 20
	maxStoredIdentities   = 1024
)

func marshalAccount(a AccountSnapshot) ([]byte, error) {
	if validateSnapshot(a) != nil || len(a.Identities) > maxStoredIdentities {
		return nil, ErrCorruptState
	}
	pb := &accountpb.DBAccount{Status: accountpb.AccountStatus(a.Status), IdentityRevision: a.IdentityRevision, SessionEpoch: a.SessionEpoch}
	for _, ref := range a.Identities {
		pb.Identities = append(pb.Identities, &accountpb.LoginRef{AuthorityId: ref.AuthorityID, SubjectDigest: append([]byte(nil), ref.SubjectDigest[:]...)})
	}
	return proto.Marshal(pb)
}

func unmarshalAccount(raw []byte, id int64) (AccountSnapshot, error) {
	var pb accountpb.DBAccount
	if len(raw) == 0 || len(raw) > maxAccountRecordBytes || proto.Unmarshal(raw, &pb) != nil || len(pb.Identities) > maxStoredIdentities {
		return AccountSnapshot{}, ErrCorruptState
	}
	a := AccountSnapshot{AccountID: id, Status: AccountStatus(pb.Status), IdentityRevision: pb.IdentityRevision, SessionEpoch: pb.SessionEpoch}
	// Validate before narrowing the proto int32 enum to uint8.
	if pb.Status != accountpb.AccountStatus_ACCOUNT_STATUS_ACTIVE && pb.Status != accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED {
		return AccountSnapshot{}, ErrCorruptState
	}
	for _, identity := range pb.Identities {
		if identity == nil || len(identity.SubjectDigest) != 32 {
			return AccountSnapshot{}, ErrCorruptState
		}
		ref := LoginRef{AuthorityID: identity.AuthorityId}
		copy(ref.SubjectDigest[:], identity.SubjectDigest)
		a.Identities = append(a.Identities, ref)
	}
	if err := validateSnapshot(a); err != nil {
		return AccountSnapshot{}, err
	}
	return a, nil
}

func marshalSession(session LoginSession) ([]byte, error) {
	if !validSessionRef(session.Ref) || session.SessionEpoch == 0 || session.ExpiresAt.Unix() <= 0 {
		return nil, ErrCorruptState
	}
	return proto.Marshal(&accountpb.DBLoginSession{AccountId: session.Ref.AccountID, SessionEpoch: session.SessionEpoch, ExpiresAtUnix: session.ExpiresAt.Unix()})
}

func (s *RedisStore) loadSession(ctx context.Context, ref SessionRef) (LoginSession, error) {
	if !validSessionRef(ref) {
		return LoginSession{}, ErrSessionInvalid
	}
	raw, err := s.client.Get(ctx, s.sessionKey(ref.SessionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return LoginSession{}, errors.Join(ErrSessionInvalid, redis.Nil)
	}
	if err != nil {
		return LoginSession{}, unavailableRead("read session", err)
	}
	var pb accountpb.DBLoginSession
	if len(raw) == 0 || len(raw) > 1024 || proto.Unmarshal(raw, &pb) != nil || pb.AccountId <= 0 || pb.SessionEpoch == 0 || pb.ExpiresAtUnix <= 0 {
		return LoginSession{}, ErrCorruptState
	}
	if pb.AccountId != ref.AccountID {
		return LoginSession{}, ErrSessionInvalid
	}
	return LoginSession{Ref: ref, SessionEpoch: pb.SessionEpoch, ExpiresAt: time.Unix(pb.ExpiresAtUnix, 0)}, nil
}

func (s *RedisStore) loadLoginAccountID(ctx context.Context, ref LoginRef) (int64, error) {
	value, err := s.client.Get(ctx, s.loginKey(ref)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, ErrAccountNotFound
	}
	if err != nil {
		return 0, unavailableRead("read identity index", err)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || formatAccountID(id) != value {
		return 0, ErrCorruptState
	}
	return id, nil
}
