package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	algorithmHS256 = "HS256"
	tokenParts     = 3
)

// Key is a local signing or verification key. Secret is copied by constructors.
type Key struct {
	ID     string
	Secret []byte
}

// Options configure clock behavior for signing or verifying tokens.
type Options struct {
	Now func() time.Time
}

// SignOptions configure the token validity window.
type SignOptions struct {
	IssuedAt  int64
	ExpiresAt int64
}

// Signer signs business-owned login payload bytes.
type Signer struct {
	key Key
	now func() time.Time
}

// Verifier verifies login tokens against business-owned login payload bytes.
type Verifier struct {
	keys map[string][]byte
	now  func() time.Time
}

type header struct {
	Type      string `json:"typ"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type body struct {
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
}

// NewSigner creates a HMAC signer for dev-style payload tokens.
func NewSigner(key Key, opts Options) (*Signer, error) {
	copied, err := validateKey(key)
	if err != nil {
		return nil, err
	}
	return &Signer{key: copied, now: clock(opts.Now)}, nil
}

// NewVerifier creates a local verifier with one or more accepted signing keys.
func NewVerifier(keys []Key, opts Options) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys configured", ErrUnknownKey)
	}
	accepted := make(map[string][]byte, len(keys))
	for _, key := range keys {
		copied, err := validateKey(key)
		if err != nil {
			return nil, err
		}
		if _, exists := accepted[copied.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate key id %q", ErrInvalidToken, copied.ID)
		}
		accepted[copied.ID] = copied.Secret
	}
	return &Verifier{keys: accepted, now: clock(opts.Now)}, nil
}

// Sign signs payload bytes without embedding the payload into the token.
func (s *Signer) Sign(payload []byte, opts SignOptions) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: signer is nil", ErrInvalidToken)
	}
	if len(payload) == 0 {
		return "", ErrInvalidPayload
	}
	now := s.now().Unix()
	if opts.IssuedAt == 0 {
		opts.IssuedAt = now
	}
	if opts.ExpiresAt <= now {
		return "", ErrExpired
	}
	if opts.IssuedAt > opts.ExpiresAt {
		return "", fmt.Errorf("%w: issuedAt after expiresAt", ErrInvalidToken)
	}
	head, err := encodeJSON(header{Type: "AIFD-PAYLOAD-TOKEN", Algorithm: algorithmHS256, KeyID: s.key.ID})
	if err != nil {
		return "", err
	}
	meta, err := encodeJSON(body{IssuedAt: opts.IssuedAt, ExpiresAt: opts.ExpiresAt})
	if err != nil {
		return "", err
	}
	signingInput := buildSigningInput(head, meta, payload)
	sig := signHMAC([]byte(signingInput), s.key.Secret)
	return head + "." + meta + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify validates a token against payload bytes and checks its validity window.
func (v *Verifier) Verify(payload []byte, token string) error {
	if v == nil {
		return fmt.Errorf("%w: verifier is nil", ErrInvalidToken)
	}
	if len(payload) == 0 {
		return ErrInvalidPayload
	}
	parts := strings.Split(token, ".")
	if len(parts) != tokenParts {
		return ErrInvalidToken
	}
	var head header
	if err := decodeJSON(parts[0], &head); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if head.Algorithm != algorithmHS256 || head.KeyID == "" {
		return ErrInvalidToken
	}
	secret, ok := v.keys[head.KeyID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, head.KeyID)
	}
	var meta body
	if err := decodeJSON(parts[1], &meta); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if meta.ExpiresAt <= v.now().Unix() {
		return ErrExpired
	}
	if meta.IssuedAt > meta.ExpiresAt {
		return fmt.Errorf("%w: issuedAt after expiresAt", ErrInvalidToken)
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	wantSig := signHMAC([]byte(buildSigningInput(parts[0], parts[1], payload)), secret)
	if !hmac.Equal(gotSig, wantSig) {
		return ErrInvalidToken
	}
	return nil
}

func buildSigningInput(head string, meta string, payload []byte) string {
	return head + "." + meta + "." + base64.RawURLEncoding.EncodeToString(payload)
}

func validateKey(key Key) (Key, error) {
	if key.ID == "" {
		return Key{}, fmt.Errorf("%w: key id is empty", ErrInvalidToken)
	}
	if len(key.Secret) == 0 {
		return Key{}, fmt.Errorf("%w: key secret is empty", ErrInvalidToken)
	}
	secret := append([]byte(nil), key.Secret...)
	return Key{ID: key.ID, Secret: secret}, nil
}

func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeJSON(part string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func signHMAC(data []byte, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func clock(now func() time.Time) func() time.Time {
	if now != nil {
		return now
	}
	return time.Now
}
