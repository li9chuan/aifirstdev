package wechat

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testBinding() credential.ChallengeBinding {
	return credential.ChallengeBinding{Purpose: "external_uuid_login", ScopeDigest: sha256.Sum256([]byte("external-uuid-context"))}
}
func opts(connector string) Options {
	return Options{ConnectorID: connector, AuthorityID: "verified-platform", AppID: "wx-test", AppSecret: "server-secret", SubjectKind: SubjectUnionID}
}

func testHTTPClient(t *testing.T, handler http.HandlerFunc) *HTTPClient {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Host)
	}
	return NewHTTPClient(&http.Client{Transport: transport})
}

func TestWebsiteStateBindingAndDirectMiniCodeShareVerifiedAuthority(t *testing.T) {
	var calls atomic.Int32
	client := testHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Get("appid") != "wx-test" || q.Get("secret") != "server-secret" || q.Get("grant_type") != "authorization_code" {
			t.Error("server configuration lost")
		}
		switch r.URL.Path {
		case "/sns/jscode2session":
			if q.Get("js_code") != "mini-code" {
				t.Error("missing mini code")
			}
			fmt.Fprint(w, `{"openid":"mini-open","unionid":"common-union","session_key":"never-exposed"}`)
		case "/sns/oauth2/access_token":
			if q.Get("code") != "web-code" {
				t.Error("missing web code")
			}
			fmt.Fprint(w, `{"openid":"web-open","unionid":"common-union","access_token":"never-exposed"}`)
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	})
	now := time.Now()
	website, err := NewWebsiteProvider(client, NewMemoryWebsiteStore(), WebsiteOptions{Options: opts("web"), Redirects: map[string]string{"return": "https://example.com/callback"}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := website.BeginAuthorization(context.Background(), WebsiteAuthorizationRequest{Binding: testBinding(), RedirectID: "return"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ch.AuthorizationURL)
	if err != nil || u.Host != "open.weixin.qq.com" || u.Query().Get("scope") != "snsapi_login" || u.Query().Get("redirect_uri") != "https://example.com/callback" {
		t.Fatal("authorization URL", u, err)
	}
	proof := WebsiteProof{ChallengeID: ch.ID, Code: "web-code", State: u.Query().Get("state")}
	req := credential.VerifyRequest{Binding: testBinding(), Credential: EncodeWebsiteProof("web", proof)}
	wrong := req
	wrong.Binding.Purpose = "bind"
	if _, err = website.Verify(context.Background(), wrong); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
	wrongProof := proof
	wrongProof.State = strings.Repeat("a", 32)
	wrong.Credential = EncodeWebsiteProof("web", wrongProof)
	wrong.Binding = req.Binding
	if _, err = website.Verify(context.Background(), wrong); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid state called authority")
	}
	webPrincipal, err := website.Verify(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = website.Verify(context.Background(), req); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal("state replay", err)
	}
	mini, err := NewMiniProgramProvider(client, opts("mini"))
	if err != nil {
		t.Fatal(err)
	}
	miniPrincipal, err := mini.Verify(context.Background(), credential.VerifyRequest{Credential: EncodeMiniProgramProof("mini", MiniProgramProof{Code: "mini-code"})})
	if err != nil || miniPrincipal != webPrincipal {
		t.Fatal("shared authority mapping", miniPrincipal, webPrincipal, err)
	}
	if calls.Load() != 2 {
		t.Fatal("code exchange count", calls.Load())
	}
}

func TestWebsiteConcurrentConsumeExpiryAndUnknownExchange(t *testing.T) {
	var calls atomic.Int32
	client := testHTTPClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	})
	now := time.Now()
	p, err := NewWebsiteProvider(client, NewMemoryWebsiteStore(), WebsiteOptions{Options: opts("web"), Redirects: map[string]string{"r": "https://example.com/cb"}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.BeginAuthorization(context.Background(), WebsiteAuthorizationRequest{Binding: testBinding(), RedirectID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(ch.AuthorizationURL)
	req := credential.VerifyRequest{Binding: testBinding(), Credential: EncodeWebsiteProof("web", WebsiteProof{ChallengeID: ch.ID, Code: "code", State: u.Query().Get("state")})}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() { _, _ = p.Verify(context.Background(), req) })
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("ambiguous exchange replayed", calls.Load())
	}
	ch, err = p.BeginAuthorization(context.Background(), WebsiteAuthorizationRequest{Binding: testBinding(), RedirectID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(ch.AuthorizationURL)
	now = ch.ExpiresAt
	req.Credential = EncodeWebsiteProof("web", WebsiteProof{ChallengeID: ch.ID, Code: "code", State: u.Query().Get("state")})
	if _, err = p.Verify(context.Background(), req); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal("expired state", err)
	}
}

func TestMiniCodeRejectsInvalidResponseAndIdentityAssertions(t *testing.T) {
	for _, response := range []string{`{"errcode":40029}`, `{"openid":"open","session_key":"key"}`, `{"openid":"open","unionid":"union"}`, `bad-json`} {
		t.Run(response, func(t *testing.T) {
			client := testHTTPClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, response) })
			p, err := NewMiniProgramProvider(client, opts("mini"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Verify(context.Background(), credential.VerifyRequest{Credential: EncodeMiniProgramProof("mini", MiniProgramProof{Code: "code"})}); err == nil {
				t.Fatal("invalid upstream identity accepted")
			}
		})
	}
	var called atomic.Bool
	client := testHTTPClient(t, func(http.ResponseWriter, *http.Request) { called.Store(true) })
	p, _ := NewMiniProgramProvider(client, opts("mini"))
	if _, err := p.Verify(context.Background(), credential.VerifyRequest{Credential: credential.Credential{ConnectorID: "mini", Proof: []byte(`{"code":"code","subject":"forged"}`)}}); !errors.Is(err, credential.ErrInvalidCredential) || called.Load() {
		t.Fatal("asserted identity accepted", err)
	}
}

func TestHTTPClientDoesNotFollowRedirectsOrLeakSecrets(t *testing.T) {
	var requests atomic.Int32
	client := NewHTTPClient(&http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://attacker.invalid/capture"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})})
	_, err := client.ExchangeMiniProgramCode(context.Background(), "app", "secret", "code")
	if !errors.Is(err, credential.ErrUnavailable) || strings.Contains(err.Error(), "secret") || requests.Load() != 1 {
		t.Fatal("redirect/error leakage", err, requests.Load())
	}
}
