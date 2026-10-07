package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	redisgo "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	defaultKeyPrefix = "aifd:mail"
	metaField        = "meta"
	versionField     = "version"
)

// RedisStoreOptions configures Redis key naming.
type RedisStoreOptions struct {
	KeyPrefix string
}

// RedisStore uses caller-owned Redis as the production mail truth source.
type RedisStore struct {
	client redisgo.UniversalClient
	prefix string
}

// NewRedisStore creates a Redis-backed mail store without taking ownership of the client lifecycle.
func NewRedisStore(client redisgo.UniversalClient, opts RedisStoreOptions) (*RedisStore, error) {
	if client == nil {
		return nil, fmt.Errorf("mail redis client is nil")
	}
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = defaultKeyPrefix
	}
	return &RedisStore{client: client, prefix: opts.KeyPrefix}, nil
}

// PersonalMailboxVersion reads the durable revision without loading bodies.
func (s *RedisStore) PersonalMailboxVersion(ctx context.Context, rootID int64) (uint64, error) {
	version, err := s.client.HGet(ctx, s.userMetaKey(rootID), versionField).Uint64()
	if errors.Is(err, redisgo.Nil) {
		return 0, nil
	}
	return version, err
}

// LoadPersonalMailbox loads an atomic snapshot of payloads, metadata and version.
func (s *RedisStore) LoadPersonalMailbox(ctx context.Context, rootID int64) (*PersonalMailbox, error) {
	pipe := s.client.TxPipeline()
	dataCmd := pipe.HGetAll(ctx, s.userDataKey(rootID))
	metaCmd := pipe.HGet(ctx, s.userMetaKey(rootID), metaField)
	versionCmd := pipe.HGet(ctx, s.userMetaKey(rootID), versionField)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisgo.Nil) {
		return nil, err
	}
	box := &PersonalMailbox{Meta: &mailpb.MailboxMeta{}}
	values, err := dataCmd.Result()
	if err != nil {
		return nil, err
	}
	box.Mails = make([]*mailpb.MailData, 0, len(values))
	for _, value := range values {
		data, err := unmarshalMail([]byte(value))
		if err != nil {
			return nil, err
		}
		box.Mails = append(box.Mails, data)
	}
	if value, err := metaCmd.Bytes(); err == nil {
		if err := proto.Unmarshal(value, box.Meta); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, redisgo.Nil) {
		return nil, err
	}
	if version, err := versionCmd.Uint64(); err == nil {
		box.Version = version
	} else if !errors.Is(err, redisgo.Nil) {
		return nil, err
	}
	return box, nil
}

// ApplyPersonalMailbox atomically applies a version-checked mailbox delta.
func (s *RedisStore) ApplyPersonalMailbox(ctx context.Context, rootID int64, expectedVersion uint64, change PersonalMailboxChange) (uint64, error) {
	upsert := make(map[string]any, len(change.Upsert))
	for _, data := range change.Upsert {
		payload, err := marshalMail(data)
		if err != nil {
			return expectedVersion, err
		}
		upsert[strconv.FormatInt(data.GetMailId(), 10)] = payload
	}
	deleteFields := make([]string, 0, len(change.DeleteIDs))
	for _, mailID := range change.DeleteIDs {
		deleteFields = append(deleteFields, strconv.FormatInt(mailID, 10))
	}
	var metaPayload []byte
	var err error
	if change.Meta != nil {
		metaPayload, err = proto.Marshal(CloneMeta(change.Meta))
		if err != nil {
			return expectedVersion, err
		}
	}
	newVersion := expectedVersion + 1
	err = s.client.Watch(ctx, func(tx *redisgo.Tx) error {
		current, err := tx.HGet(ctx, s.userMetaKey(rootID), versionField).Uint64()
		if errors.Is(err, redisgo.Nil) {
			current = 0
		} else if err != nil {
			return err
		}
		if current != expectedVersion {
			return ErrVersionConflict
		}
		_, err = tx.TxPipelined(ctx, func(pipe redisgo.Pipeliner) error {
			if len(upsert) > 0 {
				pipe.HSet(ctx, s.userDataKey(rootID), upsert)
			}
			if len(deleteFields) > 0 {
				pipe.HDel(ctx, s.userDataKey(rootID), deleteFields...)
			}
			if change.Meta != nil {
				pipe.HSet(ctx, s.userMetaKey(rootID), metaField, metaPayload)
			}
			pipe.HSet(ctx, s.userMetaKey(rootID), versionField, newVersion)
			pipe.Del(ctx, s.userIndexKey(rootID))
			if change.MailCount == 0 {
				pipe.Del(ctx, s.userDataKey(rootID))
			} else if change.DataExpiresAt > 0 {
				pipe.ExpireAt(ctx, s.userDataKey(rootID), time.Unix(change.DataExpiresAt, 0))
			} else {
				pipe.Persist(ctx, s.userDataKey(rootID))
			}
			return nil
		})
		return err
	}, s.userMetaKey(rootID))
	if errors.Is(err, redisgo.TxFailedErr) {
		return expectedVersion, ErrVersionConflict
	}
	if err != nil {
		return expectedVersion, err
	}
	return newVersion, nil
}

// SaveShared stores a global or group mail and updates its indexes.
func (s *RedisStore) SaveShared(ctx context.Context, data *mailpb.MailData) error {
	payload, err := marshalMail(data)
	if err != nil {
		return err
	}
	mailID := data.GetMailId()
	member := strconv.FormatInt(mailID, 10)
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.sharedDataKey(), member, payload)
	if data.GetType() == mailpb.MailType_MAIL_TYPE_GLOBAL {
		pipe.ZAdd(ctx, s.globalIndexKey(), redisgo.Z{Score: float64(mailID), Member: member})
	}
	for _, groupKey := range data.GetGroupKeys() {
		if groupKey == "" {
			continue
		}
		pipe.SAdd(ctx, s.groupSetKey(), groupKey)
		pipe.ZAdd(ctx, s.groupIndexKey(groupKey), redisgo.Z{Score: float64(mailID), Member: member})
	}
	_, err = pipe.Exec(ctx)
	return err
}

// ListGlobalAfter lists global mails after a cursor.
func (s *RedisStore) ListGlobalAfter(ctx context.Context, afterMailID int64, realmID int32, limit int) ([]*mailpb.MailData, error) {
	return s.listSharedAfter(ctx, s.globalIndexKey(), afterMailID, realmID, limit)
}

// ListGroupAfter lists group mails after a cursor.
func (s *RedisStore) ListGroupAfter(ctx context.Context, groupKey string, afterMailID int64, limit int) ([]*mailpb.MailData, error) {
	return s.listSharedAfter(ctx, s.groupIndexKey(groupKey), afterMailID, 0, limit)
}

// CleanupSharedExpired removes expired shared mail payloads and indexes.
func (s *RedisStore) CleanupSharedExpired(ctx context.Context, nowUnix int64) (int, error) {
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}
	count := 0
	values, err := s.client.HGetAll(ctx, s.sharedDataKey()).Result()
	if err != nil {
		return count, err
	}
	for field, value := range values {
		data, err := unmarshalMail([]byte(value))
		if err != nil || !expired(data, nowUnix) {
			continue
		}
		mailID := data.GetMailId()
		member := strconv.FormatInt(mailID, 10)
		pipe := s.client.TxPipeline()
		pipe.HDel(ctx, s.sharedDataKey(), field)
		pipe.ZRem(ctx, s.globalIndexKey(), member)
		for _, groupKey := range data.GetGroupKeys() {
			pipe.ZRem(ctx, s.groupIndexKey(groupKey), member)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *RedisStore) listSharedAfter(ctx context.Context, indexKey string, afterMailID int64, realmID int32, limit int) ([]*mailpb.MailData, error) {
	fetchLimit := limit
	if fetchLimit <= 0 {
		fetchLimit = 100
	} else {
		fetchLimit *= 4
	}
	ids, err := s.rangeIDs(ctx, indexKey, afterMailID, fetchLimit)
	if err != nil {
		return nil, err
	}
	out := make([]*mailpb.MailData, 0, boundedLimit(limit, len(ids)))
	now := time.Now().Unix()
	for _, mailID := range ids {
		data, ok, err := s.getShared(ctx, mailID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if expired(data, now) {
			continue
		}
		if realmID > 0 && !matchRealm(data.GetTargetRealms(), realmID) {
			continue
		}
		out = append(out, data)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *RedisStore) getShared(ctx context.Context, mailID int64) (*mailpb.MailData, bool, error) {
	value, err := s.client.HGet(ctx, s.sharedDataKey(), strconv.FormatInt(mailID, 10)).Bytes()
	if errors.Is(err, redisgo.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	data, err := unmarshalMail(value)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *RedisStore) rangeIDs(ctx context.Context, key string, afterMailID int64, limit int) ([]int64, error) {
	query := &redisgo.ZRangeBy{
		Min: fmt.Sprintf("(%d", afterMailID),
		Max: "+inf",
	}
	if limit > 0 {
		query.Count = int64(limit)
	}
	members, err := s.client.ZRangeByScore(ctx, key, query).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		id, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func marshalMail(data *mailpb.MailData) ([]byte, error) {
	if data == nil {
		return nil, fmt.Errorf("mail data is nil")
	}
	return proto.Marshal(CloneMail(data))
}

func unmarshalMail(value []byte) (*mailpb.MailData, error) {
	data := &mailpb.MailData{}
	if err := proto.Unmarshal(value, data); err != nil {
		return nil, err
	}
	return CloneMail(data), nil
}

func (s *RedisStore) userDataKey(rootID int64) string {
	return fmt.Sprintf("%s:user:%d:data", s.prefix, rootID)
}

func (s *RedisStore) userIndexKey(rootID int64) string {
	return fmt.Sprintf("%s:user:%d:index", s.prefix, rootID)
}

func (s *RedisStore) userMetaKey(rootID int64) string {
	return fmt.Sprintf("%s:user:%d:meta", s.prefix, rootID)
}

func (s *RedisStore) sharedDataKey() string {
	return s.prefix + ":shared:data"
}

func (s *RedisStore) globalIndexKey() string {
	return s.prefix + ":global:index"
}

func (s *RedisStore) groupIndexKey(groupKey string) string {
	return fmt.Sprintf("%s:group:%s:index", s.prefix, groupKey)
}

func (s *RedisStore) groupSetKey() string {
	return s.prefix + ":groups"
}
