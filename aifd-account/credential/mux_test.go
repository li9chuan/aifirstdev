package credential_test

import (
	"context"
	"errors"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

func TestMuxRoutesConnectorWithoutRequiringChallenge(t *testing.T) {
	seen := 0
	v := credential.VerifierFunc(func(_ context.Context, req credential.VerifyRequest) (credential.Principal, error) {
		seen++
		return credential.Principal{AuthorityID: "shared-issuer", Subject: "upstream-uuid"}, nil
	})
	registry := map[string]credential.Verifier{"mini": v, "website": v}
	mux, err := credential.NewVerifierMux(registry)
	if err != nil {
		t.Fatal(err)
	}
	delete(registry, "mini")
	for _, connector := range []string{"mini", "website"} {
		p, err := mux.Verify(context.Background(), credential.VerifyRequest{Credential: credential.Credential{ConnectorID: connector, Proof: []byte("code")}})
		if err != nil || p.AuthorityID != "shared-issuer" {
			t.Fatalf("principal=%+v err=%v", p, err)
		}
	}
	if seen != 2 {
		t.Fatal(seen)
	}
	_, err = mux.Verify(context.Background(), credential.VerifyRequest{Credential: credential.Credential{ConnectorID: "unregistered", Proof: []byte("code")}})
	if !errors.Is(err, credential.ErrConnectorNotAllowed) || seen != 2 {
		t.Fatal(err)
	}
}

func TestMuxKeepsChallengeValidationWithProvider(t *testing.T) {
	want := credential.ChallengeBinding{Purpose: "external-uuid-operation", ScopeDigest: [32]byte{1}}
	v := credential.VerifierFunc(func(_ context.Context, req credential.VerifyRequest) (credential.Principal, error) {
		if req.Binding != want {
			return credential.Principal{}, credential.ErrInvalidCredential
		}
		return credential.Principal{AuthorityID: "otp", Subject: "verified"}, nil
	})
	mux, err := credential.NewVerifierMux(map[string]credential.Verifier{"otp": v})
	if err != nil {
		t.Fatal(err)
	}
	req := credential.VerifyRequest{Credential: credential.Credential{ConnectorID: "otp", Proof: []byte("proof")}}
	if _, err := mux.Verify(context.Background(), req); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
	req.Binding = want
	if _, err := mux.Verify(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Credential.Proof = make([]byte, credential.MaxProofBytes+1)
	if _, err := mux.Verify(context.Background(), req); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
}
