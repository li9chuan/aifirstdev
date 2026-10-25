package rdb

import (
	"context"
	"time"
)

type store interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
	Expire(context.Context, string, time.Duration) error
	HGet(context.Context, string, string) ([]byte, bool, error)
	HSet(context.Context, string, string, []byte) error
	HDelete(context.Context, string, string) error
	HKeys(context.Context, string) ([]string, error)
	HGetAll(context.Context, string) (map[string][]byte, error)
	Persist(context.Context, string) error
}
