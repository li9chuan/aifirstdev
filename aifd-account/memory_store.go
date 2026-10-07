package account

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
)

const defaultIDStart int64 = 1000

// MemoryOptions configures in-memory account storage.
type MemoryOptions struct {
	AccountIDStart int64
}

// MemoryStore is a concurrency-safe in-memory account store for tests and local demos.
type MemoryStore struct {
	mu sync.RWMutex

	nextAccountID int64

	loginAccounts map[string]int64
	accounts      map[int64]*accountpb.DBAccount
}

// GetAccount returns one account record without requiring an active login mapping.
func (s *MemoryStore) GetAccount(_ context.Context, accountID int64) (AccountRecord, error) {
	if s == nil {
		return AccountRecord{}, ErrStoreMissing
	}
	if accountID <= 0 {
		return AccountRecord{}, ErrAccountMissing
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return accountRecordFromDB(s.accounts[accountID], accountID)
}

// NewMemoryStore creates an in-memory account store.
func NewMemoryStore(opts MemoryOptions) *MemoryStore {
	accountStart := opts.AccountIDStart
	if accountStart == 0 {
		accountStart = defaultIDStart
	}
	return &MemoryStore{
		nextAccountID: accountStart,
		loginAccounts: map[string]int64{},
		accounts:      map[int64]*accountpb.DBAccount{},
	}
}

// ResolveOrCreate resolves or creates the account for one verified login ref.
func (s *MemoryStore) ResolveOrCreate(_ context.Context, login LoginRef, autoCreate bool) (ResolveResult, error) {
	if s == nil {
		return ResolveResult{}, ErrStoreMissing
	}
	if err := validateLoginRef(login); err != nil {
		return ResolveResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	loginKey := makeLoginRefKey(login)
	if accountID, ok := s.loginAccounts[loginKey]; ok {
		if err := validateActiveAccount(s.accounts[accountID], accountID); err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{AccountID: accountID}, nil
	}
	if !autoCreate {
		return ResolveResult{}, ErrAccountMissing
	}

	s.nextAccountID++
	accountID := s.nextAccountID
	s.accounts[accountID] = newDBAccount(accountID, time.Now().Unix())
	s.loginAccounts[loginKey] = accountID
	return ResolveResult{AccountID: accountID, IsNewAccount: true}, nil
}

func makeLoginRefKey(login LoginRef) string {
	return login.Provider + ":" + login.SubjectHash
}

func newDBAccount(accountID int64, nowUnix int64) *accountpb.DBAccount {
	return &accountpb.DBAccount{
		AccountId:     accountID,
		Status:        accountpb.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		CreatedAtUnix: nowUnix,
		UpdatedAtUnix: nowUnix,
	}
}

func validateActiveAccount(record *accountpb.DBAccount, accountID int64) error {
	if record == nil {
		return ErrAccountMissing
	}
	if record.GetAccountId() != accountID {
		return fmt.Errorf("%w: account record id mismatch", ErrAccountMissing)
	}
	switch record.GetStatus() {
	case accountpb.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return nil
	case accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED:
		return ErrAccountDisabled
	default:
		return fmt.Errorf("%w: account status is invalid", ErrInvalidCredential)
	}
}

func accountRecordFromDB(record *accountpb.DBAccount, accountID int64) (AccountRecord, error) {
	if record == nil || record.GetAccountId() != accountID {
		return AccountRecord{}, ErrAccountMissing
	}
	status := ""
	switch record.GetStatus() {
	case accountpb.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		status = AccountStatusActive
	case accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED:
		status = AccountStatusDisabled
	default:
		return AccountRecord{}, fmt.Errorf("%w: account status is invalid", ErrInvalidCredential)
	}
	return AccountRecord{
		AccountID: record.GetAccountId(),
		Status:    status,
		CreatedAt: time.Unix(record.GetCreatedAtUnix(), 0),
		UpdatedAt: time.Unix(record.GetUpdatedAtUnix(), 0),
	}, nil
}

func formatAccountID(accountID int64) string {
	return strconv.FormatInt(accountID, 10)
}
