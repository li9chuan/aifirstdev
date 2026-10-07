package account

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const defaultRedisKeyPrefix = "account"

// RedisStoreOptions configures Redis-backed account storage.
type RedisStoreOptions struct {
	KeyPrefix      string
	AccountIDStart int64
}

// RedisStore stores account login mappings and account records directly in Redis.
type RedisStore struct {
	client redis.UniversalClient
	keys   redisStoreKeys

	accountIDStart int64
}

type redisStoreKeys struct {
	prefix     string
	accountSeq string
}

// NewRedisStore creates a Redis-backed account store using a caller-owned client.
func NewRedisStore(client redis.UniversalClient, opts RedisStoreOptions) (*RedisStore, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: redis client is nil", ErrStoreMissing)
	}
	prefix := strings.TrimRight(opts.KeyPrefix, ":")
	if prefix == "" {
		prefix = defaultRedisKeyPrefix
	}
	accountStart := opts.AccountIDStart
	if accountStart == 0 {
		accountStart = defaultIDStart
	}
	return &RedisStore{
		client: client,
		keys: redisStoreKeys{
			prefix:     prefix,
			accountSeq: prefix + ":seq:act",
		},
		accountIDStart: accountStart,
	}, nil
}

// ResolveOrCreate resolves or creates the account for one verified login ref.
func (s *RedisStore) ResolveOrCreate(ctx context.Context, login LoginRef, autoCreate bool) (ResolveResult, error) {
	if s == nil || s.client == nil {
		return ResolveResult{}, ErrStoreMissing
	}
	if err := validateLoginRef(login); err != nil {
		return ResolveResult{}, err
	}
	loginKey := s.loginKey(login)
	accountID, found, err := s.loadLoginAccountID(ctx, loginKey)
	if err != nil {
		return ResolveResult{}, err
	}
	if found {
		if _, err := s.loadActiveAccount(ctx, accountID); err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{AccountID: accountID}, nil
	}
	if !autoCreate {
		return ResolveResult{}, ErrAccountMissing
	}
	return s.createAccountForLogin(ctx, loginKey)
}

// GetAccount returns one account record without changing account state.
func (s *RedisStore) GetAccount(ctx context.Context, accountID int64) (AccountRecord, error) {
	if s == nil || s.client == nil {
		return AccountRecord{}, ErrStoreMissing
	}
	record, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return AccountRecord{}, err
	}
	return accountRecordFromDB(record, accountID)
}

func (s *RedisStore) createAccountForLogin(ctx context.Context, loginKey string) (ResolveResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		accountID, err := s.nextAccountID(ctx)
		if err != nil {
			return ResolveResult{}, err
		}
		record := newDBAccount(accountID, time.Now().Unix())
		raw, err := proto.Marshal(record)
		if err != nil {
			return ResolveResult{}, err
		}
		accountKey := s.accountKey(accountID)
		if err := s.client.Set(ctx, accountKey, raw, 0).Err(); err != nil {
			return ResolveResult{}, err
		}
		ok, err := s.client.SetNX(ctx, loginKey, formatAccountID(accountID), 0).Result()
		if err != nil {
			return ResolveResult{}, err
		}
		if ok {
			return ResolveResult{AccountID: accountID, IsNewAccount: true}, nil
		}
		_ = s.client.Del(ctx, accountKey).Err()
		existing, found, err := s.loadLoginAccountID(ctx, loginKey)
		if err != nil {
			return ResolveResult{}, err
		}
		if found {
			if _, err := s.loadActiveAccount(ctx, existing); err != nil {
				return ResolveResult{}, err
			}
			return ResolveResult{AccountID: existing}, nil
		}
	}
	return ResolveResult{}, fmt.Errorf("account redis resolve did not converge")
}

func (s *RedisStore) nextAccountID(ctx context.Context) (int64, error) {
	if err := s.client.SetNX(ctx, s.keys.accountSeq, s.accountIDStart, 0).Err(); err != nil {
		return 0, err
	}
	return s.client.Incr(ctx, s.keys.accountSeq).Result()
}

func (s *RedisStore) loadLoginAccountID(ctx context.Context, key string) (int64, bool, error) {
	value, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	accountID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || accountID <= 0 {
		return 0, false, fmt.Errorf("%w: invalid account id in login key", ErrInvalidCredential)
	}
	return accountID, true, nil
}

func (s *RedisStore) loadActiveAccount(ctx context.Context, accountID int64) (*accountpb.DBAccount, error) {
	record, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := validateActiveAccount(record, accountID); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *RedisStore) loadAccount(ctx context.Context, accountID int64) (*accountpb.DBAccount, error) {
	if accountID <= 0 {
		return nil, ErrAccountMissing
	}
	value, err := s.client.Get(ctx, s.accountKey(accountID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrAccountMissing
	}
	if err != nil {
		return nil, err
	}
	var record accountpb.DBAccount
	if err := proto.Unmarshal(value, &record); err != nil {
		return nil, fmt.Errorf("decode account record: %w", err)
	}
	return &record, nil
}

func (s *RedisStore) loginKey(login LoginRef) string {
	return s.keys.prefix + ":login:" + login.Provider + ":" + login.SubjectHash
}

func (s *RedisStore) accountKey(accountID int64) string {
	return s.keys.prefix + ":act:" + formatAccountID(accountID)
}
