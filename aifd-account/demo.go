package account

import (
	"context"
	"encoding/json"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

// ProviderDemo is a deliberately insecure connector for local examples only.
const ProviderDemo = "demo"

// DemoSecret returns the deterministic example secret; never use this for production authentication.
func DemoSecret(subject string) string { return "demo:" + subject }

// DemoCredential constructs the example's encoded proof.
func DemoCredential(subject string) credential.Credential {
	raw, _ := json.Marshal(struct{ Subject, Secret string }{subject, DemoSecret(subject)})
	return credential.Credential{ConnectorID: ProviderDemo, Proof: raw}
}

// DemoVerifier accepts deterministic development proofs only.
type DemoVerifier struct{}

// NewDemoVerifier creates the opt-in development verifier.
func NewDemoVerifier() DemoVerifier { return DemoVerifier{} }

// Verify returns a demo authority after validating the encoded proof.
func (DemoVerifier) Verify(_ context.Context, req credential.VerifyRequest) (credential.Principal, error) {
	var proof struct{ Subject, Secret string }
	if req.Credential.ConnectorID != ProviderDemo || len(req.Credential.Proof) > credential.MaxProofBytes || json.Unmarshal(req.Credential.Proof, &proof) != nil || proof.Subject == "" || proof.Secret != DemoSecret(proof.Subject) {
		return credential.Principal{}, ErrInvalidCredential
	}
	return credential.Principal{AuthorityID: ProviderDemo, Subject: proof.Subject}, nil
}

// TokenIssuerFunc adapts an application signer to the account service.
type TokenIssuerFunc func(context.Context, AuthPayload) (LoginTicket, error)

// IssueLoginToken invokes the application signer.
func (f TokenIssuerFunc) IssueLoginToken(ctx context.Context, payload AuthPayload) (LoginTicket, error) {
	return f(ctx, payload)
}

// RegistrationPolicyFunc adapts an explicit application registration policy.
type RegistrationPolicyFunc func(context.Context, credential.Principal, []byte) error

// AuthorizeRegistration invokes the application policy.
func (f RegistrationPolicyFunc) AuthorizeRegistration(ctx context.Context, p credential.Principal, proof []byte) error {
	return f(ctx, p, proof)
}
