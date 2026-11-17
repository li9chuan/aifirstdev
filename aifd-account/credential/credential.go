package credential

import "context"

// Credential carries an unverified, connector-specific bounded proof.
type Credential struct {
	ConnectorID string
	Proof       []byte
}

// Principal is an upstream identity verified by a trusted server component.
type Principal struct {
	AuthorityID string
	Subject     string
}

// ChallengeBinding associates a stored challenge with opaque caller context.
// Direct-code verification does not claim this association merely by receiving it.
type ChallengeBinding struct {
	Purpose     string
	ScopeDigest [32]byte
}

// VerifyRequest permits an empty Binding for components without a challenge.
// Challenge components must validate their stored binding themselves.
type VerifyRequest struct {
	Credential Credential
	Binding    ChallengeBinding
}

// Verifier validates a proof without depending on an account model.
type Verifier interface {
	Verify(context.Context, VerifyRequest) (Principal, error)
}
