package contracts_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/proto"
)

type authCaller struct {
	uid     int64
	headers map[string]string
}

func (c authCaller) UID() int64               { return c.uid }
func (c authCaller) Header(key string) string { return c.headers[key] }

func TestExternalIdentityWithoutAccountCenterOrSession(t *testing.T) {
	expires := time.Now().Add(time.Minute).Truncate(time.Second)
	adapter := proxy.AuthenticatorFunc(func(context.Context, proxy.AuthRequest) (proxy.Identity, error) {
		return proxy.Identity{UID: 42, Account: "external-uuid", BackendID: "existing-issuer", ValidUntil: expires}, nil
	})
	identity, err := adapter.Authenticate(context.Background(), proxy.AuthRequest{Token: "original-format-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err = identity.AuthContext().Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&proxypb.AuthContext{BackendId: identity.BackendID, Account: identity.Account, ValidUntilUnix: expires.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	caller := authCaller{uid: 42, headers: map[string]string{proxy.HeaderCallerKind: proxy.CallerClient, proxy.HeaderProxyInstanceID: "proxy-a", proxy.HeaderConnectionID: "connection-a", proxy.HeaderAuthContext: base64.RawURLEncoding.EncodeToString(raw)}}
	a, err := proxy.GetAuthContext(caller)
	if err != nil || a.Account != "external-uuid" || a.SessionID != "" || a.ValidUntil != expires {
		t.Fatalf("auth=%+v %v", a, err)
	}
	caller.headers[proxy.HeaderCallerKind] = proxy.CallerProxy
	if _, err = proxy.GetAuthContext(caller); !errors.Is(err, proxy.ErrAuthContextInvalid) {
		t.Fatal(err)
	}
}

func TestAuthContextRejectsExpiryAndMalformedMetadata(t *testing.T) {
	a := proxy.AuthContext{BackendID: "issuer", Account: "uuid", ValidUntil: time.Now().Add(-time.Second)}
	if err := a.Validate(time.Now()); !errors.Is(err, proxy.ErrAuthExpired) {
		t.Fatal(err)
	}
	if _, err := proxy.GetAuthContext(nil); !errors.Is(err, proxy.ErrAuthContextInvalid) {
		t.Fatal(err)
	}
	c := authCaller{uid: 7, headers: map[string]string{proxy.HeaderCallerKind: proxy.CallerClient, proxy.HeaderProxyInstanceID: "p", proxy.HeaderConnectionID: "c", proxy.HeaderAuthContext: "not-base64!"}}
	if _, err := proxy.GetAuthContext(c); !errors.Is(err, proxy.ErrAuthContextInvalid) {
		t.Fatal(err)
	}
}
