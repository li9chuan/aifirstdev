package rdb

import (
	"context"
	"errors"
	"sort"
	"time"

	redisgo "github.com/redis/go-redis/v9"
)

type redisStore struct {
	client redisgo.UniversalClient
}

func (s *redisStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redisgo.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *redisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, redisTTL(ttl)).Err()
}

func (s *redisStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}

func (s *redisStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	return s.client.Expire(ctx, key, ttl).Err()
}

func (s *redisStore) HGet(ctx context.Context, key, field string) ([]byte, bool, error) {
	value, err := s.client.HGet(ctx, key, field).Bytes()
	if errors.Is(err, redisgo.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *redisStore) HSet(ctx context.Context, key, field string, value []byte) error {
	return s.client.HSet(ctx, key, field, value).Err()
}

func (s *redisStore) HDelete(ctx context.Context, key, field string) error {
	return s.client.HDel(ctx, key, field).Err()
}

func (s *redisStore) HKeys(ctx context.Context, key string) ([]string, error) {
	keys, err := s.client.HKeys(ctx, key).Result()
	if errors.Is(err, redisgo.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

func redisTTL(ttl time.Duration) time.Duration {
	if ttl < 0 {
		return redisgo.KeepTTL
	}
	return ttl
}
