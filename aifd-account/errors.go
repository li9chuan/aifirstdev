package account

import (
	"errors"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

// Stable service errors are mapped to public HTTP/RPC errors by the host.
var (
	ErrInvalidCredential        = credential.ErrInvalidCredential
	ErrConnectorNotAllowed      = credential.ErrConnectorNotAllowed
	ErrRegistrationDenied       = errors.New("REGISTRATION_DENIED")
	ErrAccountNotFound          = errors.New("ACCOUNT_NOT_FOUND")
	ErrAccountDisabled          = errors.New("ACCOUNT_DISABLED")
	ErrIdentityInUse            = errors.New("IDENTITY_IN_USE")
	ErrIdentityNotBound         = errors.New("IDENTITY_NOT_BOUND")
	ErrIdentityRevisionConflict = errors.New("IDENTITY_REVISION_CONFLICT")
	ErrLastLoginIdentity        = errors.New("LAST_LOGIN_IDENTITY")
	ErrIdentityLimit            = errors.New("IDENTITY_LIMIT")
	ErrSessionInvalid           = errors.New("SESSION_INVALID")
	ErrEpochConflict            = errors.New("EPOCH_CONFLICT")
	ErrUnavailable              = credential.ErrUnavailable
	ErrResultUnknown            = errors.New("RESULT_UNKNOWN")
	ErrCorruptState             = errors.New("CORRUPT_STATE")
	ErrStoreMissing             = errors.New("account store missing")
	ErrTokenIssuerMissing       = errors.New("account token issuer missing")
)
