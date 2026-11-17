package phoneotp

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

func binding() credential.ChallengeBinding {
	return credential.ChallengeBinding{Purpose: "external-action", ScopeDigest: sha256.Sum256([]byte("original-uuid:operation"))}
}
func testProvider(t *testing.T, store Store, sender Sender, now *time.Time) *Provider {
	t.Helper()
	p, err := NewProvider(sender, store, Options{ConnectorID: "sms", AuthorityID: "phone", DigestKey: make([]byte, 32), Now: func() time.Time { return *now }, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChallengeBindingAttemptsAndOneTimeConsumption(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	var code string
	p := testProvider(t, NewMemoryStore(), SenderFunc(func(_ context.Context, phone, value string) error {
		if phone != "+8613812345678" {
			t.Fatal(phone)
		}
		code = value
		return nil
	}), &now)
	ch, err := p.RequestCode(ctx, RequestCodeInput{Phone: "+8613812345678", Binding: binding()})
	if err != nil {
		t.Fatal(err)
	}
	req := credential.VerifyRequest{Credential: EncodeProof("sms", Proof{ChallengeID: ch.ID, Code: code}), Binding: binding()}
	wrong := req
	wrong.Binding.Purpose = "another-action"
	if _, err = p.Verify(ctx, wrong); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
	if _, err = p.RequestCode(ctx, RequestCodeInput{Phone: "+8613812345678", Binding: binding()}); !errors.Is(err, ErrRateLimited) {
		t.Fatal("cooldown bypassed", err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			principal, err := p.Verify(ctx, req)
			if err == nil {
				if principal.Subject != "+8613812345678" || principal.AuthorityID != "phone" {
					t.Error(principal)
				}
				success.Add(1)
			} else if !errors.Is(err, credential.ErrInvalidCredential) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("one-time consumption", success.Load())
	}
	now = now.Add(time.Minute)
	ch, err = p.RequestCode(ctx, RequestCodeInput{Phone: "+8613812345678", Binding: binding()})
	if err != nil {
		t.Fatal(err)
	}
	bad := "000000"
	if code == bad {
		bad = "111111"
	}
	req.Credential = EncodeProof("sms", Proof{ChallengeID: ch.ID, Code: bad})
	for i := 0; i < 2; i++ {
		if _, err = p.Verify(ctx, req); !errors.Is(err, credential.ErrInvalidCredential) {
			t.Fatal(err)
		}
	}
	req.Credential = EncodeProof("sms", Proof{ChallengeID: ch.ID, Code: code})
	if _, err = p.Verify(ctx, req); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal("attempt limit ignored", err)
	}
}

func TestDeliveryFailuresAndExpiryAreFailClosed(t *testing.T) {
	for _, sendErr := range []error{errors.New("vendor failed"), ErrDeliveryUnknown} {
		t.Run(sendErr.Error(), func(t *testing.T) {
			now := time.Now()
			store := NewMemoryStore()
			var calls int
			p := testProvider(t, store, SenderFunc(func(context.Context, string, string) error { calls++; return sendErr }), &now)
			if _, err := p.RequestCode(context.Background(), RequestCodeInput{Phone: "+8613812345678", Binding: binding()}); !errors.Is(err, ErrDeliveryUnknown) {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("sender retried")
			}
			for _, r := range store.records {
				if r.active {
					t.Fatal("unknown send activated proof")
				}
			}
			if _, err := p.RequestCode(context.Background(), RequestCodeInput{Phone: "+8613812345678", Binding: binding()}); !errors.Is(err, ErrRateLimited) {
				t.Fatal("failed send removed cooldown", err)
			}
		})
	}
	now := time.Now()
	var code string
	p := testProvider(t, NewMemoryStore(), SenderFunc(func(_ context.Context, _, v string) error { code = v; return nil }), &now)
	ch, err := p.RequestCode(context.Background(), RequestCodeInput{Phone: "+8613812345678", Binding: binding()})
	if err != nil {
		t.Fatal(err)
	}
	now = ch.ExpiresAt
	if _, err = p.Verify(context.Background(), credential.VerifyRequest{Binding: binding(), Credential: EncodeProof("sms", Proof{ChallengeID: ch.ID, Code: code})}); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal("expiry ignored", err)
	}
}

func TestConcurrentSendsAndConnectorIsolation(t *testing.T) {
	now := time.Now()
	var sends atomic.Int32
	store := NewMemoryStore()
	p := testProvider(t, store, SenderFunc(func(context.Context, string, string) error { sends.Add(1); return nil }), &now)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			_, _ = p.RequestCode(context.Background(), RequestCodeInput{Phone: "+8613812345678", Binding: binding()})
		})
	}
	wg.Wait()
	if sends.Load() != 1 {
		t.Fatal("concurrent send cooldown", sends.Load())
	}
	for _, phone := range []string{"13812345678", "+0000", "+86138x2345678"} {
		if _, err := p.RequestCode(context.Background(), RequestCodeInput{Phone: phone, Binding: binding()}); !errors.Is(err, credential.ErrInvalidCredential) {
			t.Fatal(phone, err)
		}
	}
	if _, err := p.Verify(context.Background(), credential.VerifyRequest{Binding: binding(), Credential: credential.Credential{ConnectorID: "sms", Proof: []byte(`{"challenge_id":"x","code":"123456","subject":"forged"}`)}}); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
}
