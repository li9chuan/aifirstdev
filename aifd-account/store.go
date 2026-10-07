package account

import "context"

// ResolveResult describes store changes performed while resolving a principal.
type ResolveResult struct {
	AccountID    int64
	IsNewAccount bool
}

// Store owns account login mappings and account metadata.
type Store interface {
	ResolveOrCreate(context.Context, LoginRef, bool) (ResolveResult, error)
}

// AccountReader optionally exposes account status lookup without login resolution.
type AccountReader interface {
	GetAccount(context.Context, int64) (AccountRecord, error)
}
