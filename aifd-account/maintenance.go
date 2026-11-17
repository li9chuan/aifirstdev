package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// CreatedAccountCleaner is an optional offline maintenance capability. The host
// must fence all account writers and retain evidence that this account was
// created by its import. Neither the method nor its arguments provide that gate.
// This is not an online account-deletion or automatic compensation API.
type CreatedAccountCleaner interface {
	DeleteCreatedAccount(context.Context, LoginRef, int64) (bool, error)
}

func initialAccount(a AccountSnapshot, ref LoginRef) bool {
	return a.Status == AccountStatusActive && a.IdentityRevision == 1 && a.SessionEpoch == 1 && len(a.Identities) == 1 && a.Identities[0] == ref
}

// DeleteCreatedAccount removes a pristine imported account under exclusive offline
// maintenance. Revisions/epochs or identity changes reject cleanup. IDs are never
// reused, so any residual session immediately fails account validation.
func (s *MemoryStore) DeleteCreatedAccount(ctx context.Context, ref LoginRef, id int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if id <= 0 || validateLoginRef(ref) != nil {
		return false, ErrInvalidCredential
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, found := s.accounts[id]
	owner, indexed := s.loginAccounts[ref]
	if !found && !indexed {
		return false, nil
	}
	if !indexed || owner != id || (found && !initialAccount(a, ref)) {
		return false, ErrIdentityRevisionConflict
	}
	delete(s.accounts, id)
	delete(s.loginAccounts, ref)
	return true, nil
}

// DeleteCreatedAccount conditionally removes the account first, then its index.
// The two keys may be on different primaries; this is not a transaction. After a
// partial/unknown result, retain the import receipt, re-establish the offline
// fence, inspect state and call again. No write is implicitly retried.
func (s *RedisStore) DeleteCreatedAccount(ctx context.Context, ref LoginRef, id int64) (bool, error) {
	if id <= 0 || validateLoginRef(ref) != nil {
		return false, ErrInvalidCredential
	}
	a, accountErr := s.GetAccount(ctx, id)
	if accountErr != nil && !errors.Is(accountErr, ErrAccountNotFound) {
		return false, accountErr
	}
	owner, indexErr := s.loadLoginAccountID(ctx, ref)
	if errors.Is(accountErr, ErrAccountNotFound) && errors.Is(indexErr, ErrAccountNotFound) {
		return false, nil
	}
	if indexErr != nil {
		return false, indexErr
	}
	if owner != id || (accountErr == nil && !initialAccount(a, ref)) {
		return false, ErrIdentityRevisionConflict
	}
	if accountErr == nil {
		if err := s.deleteInitialAccount(ctx, ref, id); err != nil {
			return false, err
		}
	}
	if err := s.deleteIndexOnce(ctx, ref, id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *RedisStore) deleteInitialAccount(ctx context.Context, ref LoginRef, id int64) error {
	key := s.accountKey(id)
	attempted := false
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		cmd := &noRetryCmd{redis.NewCmd(ctx, "get", key)}
		if err := tx.Process(ctx, cmd); err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return unavailableRead("read maintenance account", err)
		}
		raw, err := cmd.Text()
		if err != nil {
			return unavailableRead("decode maintenance reply", err)
		}
		a, err := unmarshalAccount([]byte(raw), id)
		if err != nil {
			return err
		}
		if !initialAccount(a, ref) {
			return ErrIdentityRevisionConflict
		}
		attempted = true
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { return pipe.Process(ctx, &noRetryCmd{redis.NewCmd(ctx, "del", key)}) })
		return err
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		return fmt.Errorf("%w: account changed during cleanup", ErrIdentityRevisionConflict)
	}
	if err != nil && attempted {
		return unknownWrite("delete maintenance account", err)
	}
	return err
}
