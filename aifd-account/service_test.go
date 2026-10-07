package account

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
)

func TestLoginCreatesAndReusesAccount(t *testing.T) {
	service, issuer := newTestService(t, nil)
	ctx := context.Background()
	first, err := service.Login(ctx, LoginRequest{
		Credential: demoCredential("alice"),
		AutoCreate: true,
		TokenID:    "s1",
	})
	if err != nil {
		t.Fatalf("Login first error = %v", err)
	}
	if first.AccountID == 0 || !first.IsNewAccount || first.Token == "" {
		t.Fatalf("first = %+v, want new account token", first)
	}
	if len(issuer.payloads) != 1 {
		t.Fatalf("issued payload count = %d, want 1", len(issuer.payloads))
	}
	payload := issuer.payloads[0]
	if payload.AccountID != first.AccountID || payload.TokenID != "s1" || !payload.IsNewAccount || payload.SubjectHash == "" || payload.Provider != ProviderDemo {
		t.Fatalf("payload = %+v, want first identity", payload)
	}
	if first.Payload != payload || first.Token != "token:s1" {
		t.Fatalf("first payload/token = %+v/%q, want issuer output", first.Payload, first.Token)
	}
	second, err := service.Login(ctx, LoginRequest{
		Credential: demoCredential("alice"),
		AutoCreate: true,
	})
	if err != nil {
		t.Fatalf("Login second error = %v", err)
	}
	if second.AccountID != first.AccountID || second.IsNewAccount {
		t.Fatalf("second = %+v, want existing account %+v", second, first)
	}
	if second.Payload.TokenID == "" || second.Token != "token:"+second.Payload.TokenID {
		t.Fatalf("second payload/token = %+v/%q, want generated token id", second.Payload, second.Token)
	}
}

func TestLoginWithoutAutoCreateReturnsMissing(t *testing.T) {
	service, _ := newTestService(t, nil)
	_, err := service.Login(context.Background(), LoginRequest{Credential: demoCredential("missing")})
	if !errors.Is(err, ErrAccountMissing) {
		t.Fatalf("Login error = %v, want ErrAccountMissing", err)
	}
}

func TestLoginRejectsDisabledAccount(t *testing.T) {
	store := NewMemoryStore(MemoryOptions{})
	service, _ := newTestServiceWithStore(t, store, nil)
	result, err := service.Login(context.Background(), LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	store.mu.Lock()
	store.accounts[result.AccountID].Status = accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED
	store.mu.Unlock()
	_, err = service.Login(context.Background(), LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Login disabled error = %v, want ErrAccountDisabled", err)
	}
}

func TestAccountReaderReturnsActiveDisabledAndMissing(t *testing.T) {
	store := NewMemoryStore(MemoryOptions{})
	service, _ := newTestServiceWithStore(t, store, nil)
	result, err := service.Login(context.Background(), LoginRequest{Credential: demoCredential("reader"), AutoCreate: true})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	record, err := service.GetAccount(context.Background(), result.AccountID)
	if err != nil || record.AccountID != result.AccountID || record.Status != AccountStatusActive {
		t.Fatalf("active record=%+v err=%v", record, err)
	}
	store.mu.Lock()
	store.accounts[result.AccountID].Status = accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED
	store.mu.Unlock()
	record, err = service.GetAccount(context.Background(), result.AccountID)
	if err != nil || record.Status != AccountStatusDisabled {
		t.Fatalf("disabled record=%+v err=%v", record, err)
	}
	if _, err := service.GetAccount(context.Background(), 999999); !errors.Is(err, ErrAccountMissing) {
		t.Fatalf("missing error = %v, want ErrAccountMissing", err)
	}
}

func newTestService(t *testing.T, verifier Verifier, mutate ...func(*Options)) (*Service, *captureIssuer) {
	t.Helper()
	return newTestServiceWithStore(t, NewMemoryStore(MemoryOptions{}), verifier, mutate...)
}

func newTestServiceWithStore(t *testing.T, store Store, verifier Verifier, mutate ...func(*Options)) (*Service, *captureIssuer) {
	t.Helper()
	now := time.Unix(1000, 0)
	if verifier == nil {
		verifier = NewDemoVerifier()
	}
	issuer := &captureIssuer{}
	opts := Options{
		TokenTTL: time.Minute,
		Now:      func() time.Time { return now },
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	service, err := NewService(store, verifier, issuer, opts)
	if err != nil {
		t.Fatalf("NewService error = %v", err)
	}
	return service, issuer
}

type captureIssuer struct {
	mu       sync.Mutex
	payloads []AuthPayload
}

func (i *captureIssuer) IssueLoginToken(_ context.Context, payload AuthPayload) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.payloads = append(i.payloads, payload)
	return "token:" + payload.TokenID, nil
}

func demoCredential(subject string) Credential {
	return Credential{Provider: ProviderDemo, Subject: subject, Secret: DemoSecret(subject)}
}

func TestDemoSecret(t *testing.T) {
	if got := DemoSecret(strconv.FormatInt(1001, 10)); got != "demo:1001" {
		t.Fatalf("DemoSecret = %q", got)
	}
}
