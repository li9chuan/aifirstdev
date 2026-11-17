package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// noRetryCmd uses go-redis's per-command retry contract, including transaction
// pipelines, to prevent replay of possibly committed writes. Cluster MOVED/ASK
// replies before execution remain safely routable by the client.
type noRetryCmd struct{ *redis.Cmd }

func (*noRetryCmd) NoRetry() bool { return true }

func (s *RedisStore) writeOnce(ctx context.Context, args ...any) *redis.Cmd {
	cmd := &noRetryCmd{redis.NewCmd(ctx, args...)}
	_ = s.client.Process(ctx, cmd)
	return cmd.Cmd
}

func (s *RedisStore) createOnce(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	args := []any{"set", key, value, "nx"}
	if ttl > 0 {
		args = append(args, "px", max(int64(1), int64((ttl+time.Millisecond-1)/time.Millisecond)))
	}
	err := s.writeOnce(ctx, args...).Err()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, unknownWrite("create record", err)
	}
	return true, nil
}

func (s *RedisStore) deleteOnce(ctx context.Context, key string) error {
	if err := s.writeOnce(ctx, "del", key).Err(); err != nil {
		return unknownWrite("delete record", err)
	}
	return nil
}

// updateAccount retries only confirmed WATCH conflicts, keeping the original
// expected versions in mutate. Reads and writes inside WATCH are non-replayed.
func (s *RedisStore) updateAccount(ctx context.Context, id int64, mutate func(*AccountSnapshot) (bool, error)) (AccountSnapshot, error) {
	if id <= 0 {
		return AccountSnapshot{}, ErrAccountNotFound
	}
	key := s.accountKey(id)
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return AccountSnapshot{}, unavailableRead("account compare update", err)
		}
		var result AccountSnapshot
		attempted := false
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			cmd := &noRetryCmd{redis.NewCmd(ctx, "get", key)}
			if err := tx.Process(ctx, cmd); err != nil {
				if errors.Is(err, redis.Nil) {
					return ErrAccountNotFound
				}
				return unavailableRead("read watched account", err)
			}
			raw, err := cmd.Text()
			if err != nil {
				return ErrCorruptState
			}
			result, err = unmarshalAccount([]byte(raw), id)
			if err != nil {
				return err
			}
			changed, err := mutate(&result)
			if err != nil || !changed {
				return err
			}
			encoded, err := marshalAccount(result)
			if err != nil {
				return err
			}
			attempted = true
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				return pipe.Process(ctx, &noRetryCmd{redis.NewCmd(ctx, "set", key, encoded)})
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			if attempted {
				return AccountSnapshot{}, unknownWrite("update account", err)
			}
			return AccountSnapshot{}, err
		}
		return result, nil
	}
	return AccountSnapshot{}, fmt.Errorf("%w: account compare contention", ErrUnavailable)
}

// deleteIndexOnce never retries a conflicting WATCH or possibly committed delete.
// Re-reading the same account ID would not distinguish a later rebind.
func (s *RedisStore) deleteIndexOnce(ctx context.Context, ref LoginRef, owner int64) error {
	key := s.loginKey(ref)
	attempted := false
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		cmd := &noRetryCmd{redis.NewCmd(ctx, "get", key)}
		if err := tx.Process(ctx, cmd); err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return unavailableRead("read reserved index", err)
		}
		value, err := cmd.Text()
		if err != nil || value != formatAccountID(owner) {
			return ErrUnavailable
		}
		attempted = true
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			return pipe.Process(ctx, &noRetryCmd{redis.NewCmd(ctx, "del", key)})
		})
		return err
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		return fmt.Errorf("%w: index changed during cleanup", ErrUnavailable)
	}
	if err != nil && attempted {
		return unknownWrite("release identity index", err)
	}
	return err
}

func unknownWrite(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrResultUnknown, operation, err)
}
func unavailableRead(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
}
