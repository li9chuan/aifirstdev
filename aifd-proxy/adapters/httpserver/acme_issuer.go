package httpserver

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/mholt/acmez/v3"
	"github.com/mholt/acmez/v3/acme"
)

// managedACMEIssuer uses CertMagic's public Issuer interface so all ACME traffic
// can use a private retry transport. CertMagic still owns certificate keys,
// storage, ARI scheduling and renewal. ACMEIssuer's HTTP client is not injectable.
type managedACMEIssuer struct {
	provider    *acmeProvider
	client      *acmez.Client
	retry       *acmeRetry
	key         string
	accountKey  string
	accountMu   sync.Mutex
	account     acme.Account
	challengeMu sync.RWMutex
	challenges  map[string]*tls.Certificate
}

type storedACMEAccount struct {
	Version    int          `json:"version"`
	Account    acme.Account `json:"account"`
	PrivateKey []byte       `json:"private_key"`
}

func newManagedACMEIssuer(ctx context.Context, p *acmeProvider, directory string, roots *x509.CertPool) (*managedACMEIssuer, error) {
	u, err := url.Parse(directory)
	if err != nil || u.Host == "" || u.Scheme != "https" {
		return nil, fmt.Errorf("http acme: invalid directory")
	}
	// Match CertMagic's issuer storage key to retain its certificate layout.
	key := u.Host
	if path := strings.Trim(strings.NewReplacer("/", "-", "\\", "-").Replace(u.Path), "-"); path != "" {
		key += "-" + path
	}
	retry, err := newACMERetry(ctx, p.manager.Storage)
	if err != nil {
		return nil, err
	}
	i := &managedACMEIssuer{provider: p, retry: retry, key: key,
		accountKey: "aifd/accounts/" + certmagic.StorageKeys.Safe(key) + ".json",
		challenges: make(map[string]*tls.Certificate)}
	for _, entry := range retry.entries {
		if entry.NotBefore.After(time.Now()) && p.event != nil {
			p.event("ca_cooldown")
			break
		}
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	i.client = &acmez.Client{Client: &acme.Client{
		Directory:  directory,
		HTTPClient: &http.Client{Timeout: 30 * time.Second, Transport: &acmeRetryTransport{base: transport, retry: retry, event: p.event}},
		UserAgent:  "aifirstdev-httpserver",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, ChallengeSolvers: map[string]acmez.Solver{acme.ChallengeTypeTLSALPN01: i}}
	if err := i.loadAccount(ctx); err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return i, nil
}

func (i *managedACMEIssuer) IssuerKey() string { return i.key }

func (i *managedACMEIssuer) Issue(ctx context.Context, csr *x509.CertificateRequest) (issued *certmagic.IssuedCertificate, err error) {
	ctx, finish := i.requestContext(ctx)
	defer finish()
	names := append([]string(nil), csr.DNSNames...)
	sort.Strings(names)
	key := "certificate/" + strings.Join(names, ",")
	if err = i.retry.wait(ctx, key); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			err = errors.Join(err, i.retry.failed(key, ""))
		}
	}()
	issueCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	account, err := i.getAccount(issueCtx)
	if err != nil {
		return nil, err
	}
	params, err := acmez.OrderParametersFromCSR(account, csr)
	if err != nil {
		return nil, err
	}
	if len(names) != 0 {
		// The cache contains the actual certificate being replaced. acmez sends
		// its ARI identifier and handles the alreadyReplaced response.
		for _, cert := range i.provider.cache.AllMatchingCertificates(names[0]) {
			if cert.Leaf != nil {
				params.Replaces = cert.Leaf
				break
			}
		}
	}
	chains, err := i.client.ObtainCertificate(issueCtx, params)
	if err != nil {
		return nil, err
	}
	if len(chains) == 0 {
		return nil, fmt.Errorf("http acme: issuer returned no certificate")
	}
	if err = i.retry.succeeded(key); err != nil {
		return nil, err
	}
	return &certmagic.IssuedCertificate{Certificate: chains[0].ChainPEM, Metadata: chains[0]}, nil
}

func (i *managedACMEIssuer) GetRenewalInfo(ctx context.Context, cert certmagic.Certificate) (acme.RenewalInfo, error) {
	ctx, finish := i.requestContext(ctx)
	defer finish()
	return i.client.GetRenewalInfo(ctx, cert.Leaf)
}

func (i *managedACMEIssuer) requestContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(i.provider.ctx, cancel)
	if i.provider.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// Present and CleanUp expose challenge certificates through the existing TLS
// listener; they never open a second listener or modify global solvers.
func (i *managedACMEIssuer) Present(_ context.Context, challenge acme.Challenge) error {
	cert, err := acmez.TLSALPN01ChallengeCert(challenge)
	if err != nil {
		return err
	}
	i.challengeMu.Lock()
	i.challenges[challenge.Identifier.Value] = cert
	i.challengeMu.Unlock()
	return nil
}

func (i *managedACMEIssuer) CleanUp(_ context.Context, challenge acme.Challenge) error {
	i.challengeMu.Lock()
	delete(i.challenges, challenge.Identifier.Value)
	i.challengeMu.Unlock()
	return nil
}

func (i *managedACMEIssuer) challengeCertificate(hello *tls.ClientHelloInfo) *tls.Certificate {
	if len(hello.SupportedProtos) != 1 || hello.SupportedProtos[0] != "acme-tls/1" {
		return nil
	}
	i.challengeMu.RLock()
	defer i.challengeMu.RUnlock()
	return i.challenges[strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))]
}

func (i *managedACMEIssuer) loadAccount(ctx context.Context) error {
	data, err := i.provider.manager.Storage.Load(ctx, i.accountKey)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("http acme: read account: %w", err)
	}
	var saved storedACMEAccount
	if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
		return fmt.Errorf("http acme: invalid account")
	}
	if saved.Account.Status != "" && (saved.Account.Status != "valid" || saved.Account.Location == "") {
		return fmt.Errorf("http acme: invalid stored account status")
	}
	private, err := x509.ParsePKCS8PrivateKey(saved.PrivateKey)
	if err != nil {
		return fmt.Errorf("http acme: invalid account key")
	}
	signer, ok := private.(crypto.Signer)
	if !ok {
		return fmt.Errorf("http acme: account key cannot sign")
	}
	i.account = saved.Account
	i.account.PrivateKey = signer
	return nil
}

func (i *managedACMEIssuer) saveAccount(ctx context.Context) error {
	private, err := x509.MarshalPKCS8PrivateKey(i.account.PrivateKey)
	if err != nil {
		return err
	}
	data, err := json.Marshal(storedACMEAccount{Version: 1, Account: i.account, PrivateKey: private})
	if err != nil {
		return err
	}
	return i.provider.manager.Storage.Store(ctx, i.accountKey, data)
}

func (i *managedACMEIssuer) getAccount(ctx context.Context) (acme.Account, error) {
	i.accountMu.Lock()
	defer i.accountMu.Unlock()
	if i.account.PrivateKey == nil {
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return acme.Account{}, err
		}
		i.account = acme.Account{PrivateKey: private, TermsOfServiceAgreed: true}
		// Persist identity before registration; an interrupted registration
		// retries with the same key instead of creating another account.
		if err := i.saveAccount(ctx); err != nil {
			i.account = acme.Account{}
			return acme.Account{}, err
		}
	}
	if i.account.Status == "" {
		account, err := i.client.NewAccount(ctx, i.account)
		if err != nil {
			return acme.Account{}, err
		}
		i.account = account
		if err := i.saveAccount(ctx); err != nil {
			return acme.Account{}, err
		}
	}
	if i.account.Status != "valid" {
		return acme.Account{}, fmt.Errorf("http acme: account is not valid")
	}
	return i.account, nil
}
