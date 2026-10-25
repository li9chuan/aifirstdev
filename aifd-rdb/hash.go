package rdb

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
)

// LoadHash decodes one exact hash key into independent messages. No partially
// decoded result is returned on corruption. Bind individual HashSavers afterwards.
func LoadHash[T proto.Message](ctx context.Context, db *DB, key string, newMessage func() T) (map[string]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := db.validate(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrKeyMissing
	}
	if newMessage == nil {
		return nil, ErrMessageMissing
	}
	values, err := db.store.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]T, len(values))
	for field, raw := range values {
		msg := newMessage()
		if isNilProtoMessage(msg) {
			return nil, ErrMessageMissing
		}
		if err := proto.Unmarshal(raw, msg); err != nil {
			return nil, fmt.Errorf("rdb hash field %q decode: %w", field, err)
		}
		out[field] = msg
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Persist removes an existing key TTL. It is synchronous lifecycle I/O: call it
// outside owner and saver locks, before publishing a newly loaded object.
func (db *DB) Persist(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.validate(); err != nil {
		return err
	}
	if key == "" {
		return ErrKeyMissing
	}
	db.stateMu.Lock()
	closing := db.closing || db.closed
	db.stateMu.Unlock()
	if closing {
		return ErrDBClosing
	}
	return db.store.Persist(ctx, key)
}
