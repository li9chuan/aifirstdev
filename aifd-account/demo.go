package account

import "context"

// DemoSecret returns the deterministic credential secret accepted by DemoVerifier.
func DemoSecret(subject string) string {
	return "demo:" + subject
}

// DemoVerifier accepts ProviderDemo credentials for examples and tests.
type DemoVerifier struct{}

// NewDemoVerifier creates the built-in development verifier.
func NewDemoVerifier() DemoVerifier {
	return DemoVerifier{}
}

// Verify validates a demo credential and returns its normalized principal.
func (DemoVerifier) Verify(_ context.Context, credential Credential) (Principal, error) {
	if credential.Provider != ProviderDemo || credential.Subject == "" || credential.Secret != DemoSecret(credential.Subject) {
		return Principal{}, ErrInvalidCredential
	}
	return Principal{Provider: credential.Provider, Subject: credential.Subject}, nil
}
