// Package phoneotp verifies one-time phone challenges independently of accounts.
package phoneotp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

var (
	ErrRateLimited     = errors.New("OTP_RATE_LIMITED")
	ErrDeliveryUnknown = errors.New("OTP_DELIVERY_UNKNOWN")
)

// Sender sends exactly once. A timeout/unknown result must not be retried by
// this adapter. The application supplies its approved SMS vendor integration.
type Sender interface {
	SendCode(context.Context, string, string) error
}
type SenderFunc func(context.Context, string, string) error

func (f SenderFunc) SendCode(ctx context.Context, phone, code string) error {
	return f(ctx, phone, code)
}

type RequestCodeInput struct {
	Phone   string
	Binding credential.ChallengeBinding
}
type Challenge struct {
	ID                 string
	ExpiresAt, RetryAt time.Time
}
type Proof struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

// EncodeProof builds the fixed wire proof; it contains no asserted identity.
func EncodeProof(connectorID string, proof Proof) credential.Credential {
	raw, _ := json.Marshal(proof)
	return credential.Credential{ConnectorID: connectorID, Proof: raw}
}

// PendingChallenge is short-lived sensitive verification state, not an account
// record. Persistent stores must protect Phone and enforce the same atomic rules.
type PendingChallenge struct {
	Challenge
	Phone, RateKey string
	Binding        credential.ChallengeBinding
	CodeDigest     [32]byte
	Attempts       int
}

// Store atomically reserves per-subject cooldown and consumes attempts/proofs.
// Pending challenges are unusable until Activate confirms successful delivery.
// Cancel never releases cooldown. Hosts sharing traffic must share one store.
type Store interface {
	Create(context.Context, PendingChallenge, time.Time) error
	Activate(context.Context, string, time.Time) error
	Cancel(context.Context, string) error
	Consume(context.Context, string, credential.ChallengeBinding, [32]byte, time.Time) (string, error)
}

type Options struct {
	ConnectorID, AuthorityID string
	DigestKey                []byte
	TTL, Cooldown            time.Duration
	MaxAttempts              int
	Now                      func() time.Time
}
type Provider struct {
	sender Sender
	store  Store
	opts   Options
}

// NewProvider copies configuration and performs no sends or background work.
func NewProvider(sender Sender, store Store, opts Options) (*Provider, error) {
	if sender == nil || store == nil || !credential.ValidID(opts.ConnectorID) || !credential.ValidID(opts.AuthorityID) || len(opts.DigestKey) < 32 {
		return nil, fmt.Errorf("invalid phone OTP configuration")
	}
	if opts.TTL == 0 {
		opts.TTL = 5 * time.Minute
	}
	if opts.Cooldown == 0 {
		opts.Cooldown = time.Minute
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 5
	}
	if opts.TTL < time.Second || opts.TTL > 15*time.Minute || opts.Cooldown < time.Second || opts.MaxAttempts < 1 || opts.MaxAttempts > 10 {
		return nil, fmt.Errorf("invalid phone OTP limits")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.DigestKey = append([]byte(nil), opts.DigestKey...)
	return &Provider{sender: sender, store: store, opts: opts}, nil
}

// RequestCode reserves cooldown before sending. Failed or uncertain delivery
// cannot produce an active proof; activation failure never reports success.
func (p *Provider) RequestCode(ctx context.Context, in RequestCodeInput) (Challenge, error) {
	phone := strings.TrimSpace(in.Phone)
	if !validPhone(phone) || !validBinding(in.Binding) {
		return Challenge{}, credential.ErrInvalidCredential
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return Challenge{}, credential.ErrUnavailable
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return Challenge{}, credential.ErrUnavailable
	}
	code := fmt.Sprintf("%06d", n.Int64())
	now := p.opts.Now()
	id := hex.EncodeToString(randomID[:])
	challenge := Challenge{ID: id, ExpiresAt: now.Add(p.opts.TTL), RetryAt: now.Add(p.opts.Cooldown)}
	rate := p.digest("phone", phone)
	record := PendingChallenge{Challenge: challenge, Phone: phone, RateKey: p.opts.ConnectorID + ":" + hex.EncodeToString(rate[:]), Binding: in.Binding, CodeDigest: p.digest(id, code), Attempts: p.opts.MaxAttempts}
	record.ID = p.opts.ConnectorID + ":" + id
	if err = p.store.Create(ctx, record, now); err != nil {
		return Challenge{}, err
	}
	if err = p.sender.SendCode(ctx, phone, code); err != nil {
		// Do not expose vendor errors, which may contain phone numbers or codes.
		if !errors.Is(err, ErrDeliveryUnknown) {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = p.store.Cancel(cleanup, record.ID)
		}
		return Challenge{}, ErrDeliveryUnknown
	}
	if err = p.store.Activate(ctx, record.ID, p.opts.Now()); err != nil {
		return Challenge{}, credential.ErrUnavailable
	}
	return challenge, nil
}

// Verify consumes one active challenge only under its exact purpose and scope.
func (p *Provider) Verify(ctx context.Context, req credential.VerifyRequest) (credential.Principal, error) {
	var proof Proof
	if req.Credential.ConnectorID != p.opts.ConnectorID || !validBinding(req.Binding) || len(req.Credential.Proof) > credential.MaxProofBytes {
		return credential.Principal{}, credential.ErrInvalidCredential
	}
	d := json.NewDecoder(bytes.NewReader(req.Credential.Proof))
	d.DisallowUnknownFields()
	if d.Decode(&proof) != nil || d.Decode(new(any)) != io.EOF || len(proof.ChallengeID) != 32 || !sixDigits(proof.Code) {
		return credential.Principal{}, credential.ErrInvalidCredential
	}
	phone, err := p.store.Consume(ctx, p.opts.ConnectorID+":"+proof.ChallengeID, req.Binding, p.digest(proof.ChallengeID, proof.Code), p.opts.Now())
	if err != nil {
		return credential.Principal{}, err
	}
	return credential.Principal{AuthorityID: p.opts.AuthorityID, Subject: phone}, nil
}

func (p *Provider) digest(scope, value string) [32]byte {
	h := hmac.New(sha256.New, p.opts.DigestKey)
	_, _ = h.Write([]byte(p.opts.ConnectorID + "\x00" + scope + "\x00" + value))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func validPhone(s string) bool {
	if len(s) < 9 || len(s) > 16 || s[0] != '+' || s[1] < '1' || s[1] > '9' {
		return false
	}
	for _, c := range s[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func validBinding(b credential.ChallengeBinding) bool {
	return credential.ValidID(b.Purpose) && b.ScopeDigest != ([32]byte{})
}
func sixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
