package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// ProviderDemo is the built-in development provider used by examples and tests.
	ProviderDemo          = "demo"
	AccountStatusActive   = "active"
	AccountStatusDisabled = "disabled"
)

// AccountRecord is the public account-center view of one durable account.
type AccountRecord struct {
	AccountID int64
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Credential is provider login input accepted by account center.
type Credential struct {
	Provider string
	Subject  string
	Secret   string
}

// Principal is the normalized provider identity after account center verification.
type Principal struct {
	Provider string
	Subject  string
}

// LoginRef is the storage-safe reference for one verified provider identity.
type LoginRef struct {
	Provider    string
	SubjectHash string
}

// AuthPayload is account center's provider-neutral login payload.
type AuthPayload struct {
	AccountID    int64
	Provider     string
	SubjectHash  string
	TokenID      string
	IssuedAt     int64
	ExpiresAt    int64
	IsNewAccount bool
}

// LoginRequest asks account center to resolve or create an account.
type LoginRequest struct {
	Credential Credential
	AutoCreate bool
	TokenID    string
	TTL        time.Duration
}

// LoginResult returns the resolved identity plus a signed proxy login token.
type LoginResult struct {
	AccountID    int64
	Provider     string
	SubjectHash  string
	TokenID      string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	IsNewAccount bool
	Payload      AuthPayload
	Token        string
}

// Verifier turns provider credentials into a stable principal.
type Verifier interface {
	Verify(context.Context, Credential) (Principal, error)
}

// VerifierFunc adapts a function into a Verifier.
type VerifierFunc func(context.Context, Credential) (Principal, error)

// Verify invokes the wrapped verifier function.
func (f VerifierFunc) Verify(ctx context.Context, credential Credential) (Principal, error) {
	return f(ctx, credential)
}

// TokenIssuer signs an account login payload without coupling account core to business protobuf.
type TokenIssuer interface {
	IssueLoginToken(context.Context, AuthPayload) (string, error)
}

// TokenIssuerFunc adapts a function into a TokenIssuer.
type TokenIssuerFunc func(context.Context, AuthPayload) (string, error)

// IssueLoginToken invokes the wrapped token issuer function.
func (f TokenIssuerFunc) IssueLoginToken(ctx context.Context, payload AuthPayload) (string, error) {
	return f(ctx, payload)
}

// LoginRefFromPrincipal creates the storage key material for a verified principal.
func LoginRefFromPrincipal(principal Principal) (LoginRef, error) {
	if err := validatePrincipal(principal); err != nil {
		return LoginRef{}, err
	}
	provider := strings.TrimSpace(principal.Provider)
	subject := strings.TrimSpace(principal.Subject)
	sum := sha256.Sum256([]byte(provider + ":" + subject))
	ref := LoginRef{
		Provider:    provider,
		SubjectHash: hex.EncodeToString(sum[:]),
	}
	if err := validateLoginRef(ref); err != nil {
		return LoginRef{}, err
	}
	return ref, nil
}

func validatePrincipal(principal Principal) error {
	if strings.TrimSpace(principal.Provider) == "" || strings.TrimSpace(principal.Subject) == "" {
		return fmt.Errorf("%w: provider and subject are required", ErrInvalidCredential)
	}
	return nil
}

func validateLoginRef(ref LoginRef) error {
	provider := strings.TrimSpace(ref.Provider)
	if provider == "" || strings.Contains(provider, ":") || strings.ContainsAny(provider, " \t\r\n") {
		return fmt.Errorf("%w: invalid login provider", ErrInvalidCredential)
	}
	subjectHash := strings.TrimSpace(ref.SubjectHash)
	if len(subjectHash) != 64 {
		return fmt.Errorf("%w: invalid subject hash", ErrInvalidCredential)
	}
	for _, ch := range subjectHash {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return fmt.Errorf("%w: invalid subject hash", ErrInvalidCredential)
		}
	}
	return nil
}
