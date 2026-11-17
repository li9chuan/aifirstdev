package credential

import (
	"context"
	"errors"
	"fmt"
)

// Stable verification errors never expose raw proofs or subjects.
var (
	ErrInvalidCredential   = errors.New("INVALID_CREDENTIAL")
	ErrConnectorNotAllowed = errors.New("CONNECTOR_NOT_ALLOWED")
	ErrUnavailable         = errors.New("UNAVAILABLE")
)

// MaxProofBytes bounds an encoded proof before invoking an adapter.
const MaxProofBytes = 64 << 10

// VerifierFunc adapts a host verifier without depending on the account center.
type VerifierFunc func(context.Context, VerifyRequest) (Principal, error)

// Verify invokes the host function.
func (f VerifierFunc) Verify(ctx context.Context, req VerifyRequest) (Principal, error) {
	return f(ctx, req)
}

type verifierMux struct{ verifiers map[string]Verifier }

// NewVerifierMux freezes the host registry; authority comes from the selected verifier.
func NewVerifierMux(verifiers map[string]Verifier) (Verifier, error) {
	copy := make(map[string]Verifier, len(verifiers))
	for id, verifier := range verifiers {
		if !ValidID(id) || verifier == nil {
			return nil, fmt.Errorf("%w: invalid configuration", ErrConnectorNotAllowed)
		}
		copy[id] = verifier
	}
	return &verifierMux{verifiers: copy}, nil
}

func (m *verifierMux) Verify(ctx context.Context, req VerifyRequest) (Principal, error) {
	verifier, ok := m.verifiers[req.Credential.ConnectorID]
	if !ok {
		return Principal{}, ErrConnectorNotAllowed
	}
	if len(req.Credential.Proof) == 0 || len(req.Credential.Proof) > MaxProofBytes {
		return Principal{}, ErrInvalidCredential
	}
	principal, err := verifier.Verify(ctx, req)
	if err != nil {
		return Principal{}, err
	}
	if !ValidID(principal.AuthorityID) || len(principal.Subject) == 0 || len(principal.Subject) > MaxProofBytes {
		return Principal{}, ErrInvalidCredential
	}
	return principal, nil
}

// ValidID accepts bounded ASCII configuration identifiers, never URLs or Redis syntax.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.') {
			return false
		}
	}
	return true
}
