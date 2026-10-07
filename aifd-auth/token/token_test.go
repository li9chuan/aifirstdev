package token

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerifyPayload(t *testing.T) {
	now := time.Unix(1000, 0)
	key := Key{ID: "k1", Secret: []byte("secret")}
	signer, err := NewSigner(key, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}
	verifier, err := NewVerifier([]Key{key}, Options{Now: func() time.Time { return now.Add(time.Second) }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	payload := []byte("account=1001&uid=2001")
	token, err := signer.Sign(payload, SignOptions{ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}
	if err := verifier.Verify(payload, token); err != nil {
		t.Fatalf("Verify error = %v", err)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	now := time.Unix(1000, 0)
	key := Key{ID: "k1", Secret: []byte("secret")}
	signer, err := NewSigner(key, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}
	token, err := signer.Sign([]byte("uid=1001"), SignOptions{ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	verifier, err := NewVerifier([]Key{key}, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	if err := verifier.Verify([]byte("uid=1002"), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	now := time.Unix(1000, 0)
	key := Key{ID: "k1", Secret: []byte("secret")}
	signer, err := NewSigner(key, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}
	payload := []byte("uid=1001")
	token, err := signer.Sign(payload, SignOptions{ExpiresAt: now.Add(time.Second).Unix()})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	verifier, err := NewVerifier([]Key{key}, Options{Now: func() time.Time { return now.Add(2 * time.Second) }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	if err := verifier.Verify(payload, token); !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify error = %v, want ErrExpired", err)
	}
}

func TestVerifyRejectsTamperedOrUnknownKeyToken(t *testing.T) {
	now := time.Unix(1000, 0)
	key := Key{ID: "k1", Secret: []byte("secret")}
	signer, err := NewSigner(key, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}
	payload := []byte("uid=1001")
	token, err := signer.Sign(payload, SignOptions{ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	verifier, err := NewVerifier([]Key{key}, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	tampered := token[:len(token)-1] + "x"
	if err := verifier.Verify(payload, tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered Verify error = %v, want ErrInvalidToken", err)
	}
	otherVerifier, err := NewVerifier([]Key{{ID: "other", Secret: []byte("secret")}}, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	if err := otherVerifier.Verify(payload, token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key Verify error = %v, want ErrUnknownKey", err)
	}
}
