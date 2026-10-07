package account

import "errors"

var (
	// ErrInvalidCredential indicates that provider login input is missing or rejected.
	ErrInvalidCredential = errors.New("invalid account credential")
	// ErrAccountMissing indicates that a login principal has no account and auto-create is disabled.
	ErrAccountMissing = errors.New("account missing")
	// ErrIdentityConflict indicates that a provider identity is already bound to another account.
	ErrIdentityConflict = errors.New("account identity conflict")
	// ErrAccountDisabled indicates that an account exists but is not allowed to log in.
	ErrAccountDisabled = errors.New("account disabled")
	// ErrStoreMissing indicates that account service was created without a store.
	ErrStoreMissing = errors.New("account store missing")
	// ErrTokenIssuerMissing indicates that account service cannot issue login tokens.
	ErrTokenIssuerMissing = errors.New("account token issuer missing")
)
