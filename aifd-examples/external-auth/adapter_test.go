package externalauth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	codec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	user "github.com/li9chuan/aifirstdev/aifd-runtime/user"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// This fixture is the original issuer's deliberately small signed-token format,
// with original session/status state. It does not import AIFD auth or account.
type originalIssuer struct {
	key      []byte
	disabled atomic.Bool
	revoked  atomic.Bool
	queries  atomic.Int32
}

func (i *originalIssuer) sign(t *testing.T, v VerifiedToken) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	h := hmac.New(sha256.New, i.key)
	_, _ = h.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (i *originalIssuer) Verify(_ context.Context, token string) (VerifiedToken, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return VerifiedToken{}, ErrDenied
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return VerifiedToken{}, ErrDenied
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return VerifiedToken{}, ErrDenied
	}
	h := hmac.New(sha256.New, i.key)
	_, _ = h.Write(raw)
	if !hmac.Equal(sig, h.Sum(nil)) {
		return VerifiedToken{}, ErrDenied
	}
	var v VerifiedToken
	if json.Unmarshal(raw, &v) != nil || !time.Now().Before(v.ExpiresAt) {
		return VerifiedToken{}, ErrDenied
	}
	return v, nil
}
func (i *originalIssuer) check(_ context.Context, account, session string) error {
	i.queries.Add(1)
	if account != "external-uuid" || i.disabled.Load() || session != "" && (session != "original-session" || i.revoked.Load()) {
		return ErrDenied
	}
	return nil
}

func TestOriginalTokensReachBusinessRuntimeWithoutAccountCenter(t *testing.T) {
	for _, mode := range []proxy.AuthCheckMode{proxy.AuthCheckExpiryOnly, proxy.AuthCheckPeriodic, proxy.AuthCheckPerRequest} {
		for _, sessionID := range []string{"", "original-session"} {
			t.Run(fmt.Sprintf("mode-%d-session-%s", mode, sessionID), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				issuer := &originalIssuer{key: []byte("external-development-signing-key")}
				adapter := &Adapter{BackendID: "original-issuer", Tenant: "tenant-a", RequiredScope: "workspace", Mode: mode, Tokens: issuer, Check: issuer.check, Resolve: func(_ context.Context, account string) (int64, error) {
					if account != "external-uuid" {
						return 0, ErrDenied
					}
					return 42, nil
				}}
				db := rdb.MustNewMemoryDB()
				defer db.Close(context.Background())
				store, err := user.NewStore(user.StoreOptions[*wrapperspb.Int64Value]{DB: db, Key: func(uid int64) string { return "external-user:" + strconv.FormatInt(uid, 10) }, New: func(uid int64) *wrapperspb.Int64Value { return wrapperspb.Int64(uid) }})
				if err != nil {
					t.Fatal(err)
				}
				bus := rpc.NewLocalBus(rpc.LocalConfig{Buffer: 8})
				defer bus.Close(context.Background())
				rt := user.NewRuntime(rpc.NewRegistry(), store, nil, user.WithBus(bus))
				defer rt.Close(context.Background())
				if _, err = rt.CreateUser(ctx, 42); err != nil {
					t.Fatal(err)
				}
				if err = rt.RegisterRPC(); err != nil {
					t.Fatal(err)
				}
				if err = rt.Client("MceWho", func(s *rpc.Session, u *user.User[*wrapperspb.Int64Value], _ *wrapperspb.StringValue) {
					auth, err := proxy.GetAuthContext(s)
					if err != nil || auth.BackendID != "original-issuer" || auth.Account != "external-uuid" || auth.SessionID != sessionID || u.UID() != 42 {
						s.Error("FORBIDDEN", "wrong actor")
						return
					}
					s.Response(wrapperspb.String(auth.Account))
				}); err != nil {
					t.Fatal(err)
				}
				if err = rpc.Start(ctx, bus, rt.Registry()); err != nil {
					t.Fatal(err)
				}
				cfg := proxyws.Config{Bus: bus, AuthCheckMode: mode, HandshakeHandler: proxy.HandshakeHandlerFunc(adapter.Handshake)}
				if mode != proxy.AuthCheckExpiryOnly {
					cfg.AuthRevalidator = adapter
					cfg.AuthRecheckInterval = 30 * time.Millisecond
					cfg.AuthCheckTimeout = time.Second
				}
				wsServer := proxyws.NewServer(cfg)
				server := httptest.NewServer(wsServer)
				defer server.Close()
				defer wsServer.Close(context.Background())
				expires := time.Now().Add(1500 * time.Millisecond)
				token := issuer.sign(t, VerifiedToken{Account: "external-uuid", SessionID: sessionID, Tenant: "tenant-a", Scopes: []string{"workspace"}, ExpiresAt: expires})
				ws, _, err := coderws.Dial(ctx, "ws"+server.URL[4:], &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
				if err != nil {
					t.Fatal(err)
				}
				defer ws.CloseNow()
				body, _ := proto.Marshal(wrapperspb.String("who"))
				parts, err := codec.EncodePacket(&proxypb.MsgHead{Topic: "MceWho", PbType: "StringValue", Inbox: 1}, body)
				if err != nil {
					t.Fatal(err)
				}
				if err = ws.Write(ctx, coderws.MessageBinary, bytes.Join(parts, nil)); err != nil {
					t.Fatal(err)
				}
				_, data, err := ws.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				head, body, err := codec.DecodePacket(data)
				if err != nil {
					t.Fatal(err)
				}
				var reply wrapperspb.StringValue
				if proto.Unmarshal(body, &reply) != nil || head.Topic != "StringValue" || reply.Value != "external-uuid" {
					t.Fatal("business runtime reply", head, &reply)
				}
				if mode == proxy.AuthCheckExpiryOnly {
					if issuer.queries.Load() != 0 {
						t.Fatal("expiry-only queried authority")
					}
				} else {
					if sessionID == "" {
						issuer.disabled.Store(true)
					} else {
						issuer.revoked.Store(true)
					}
				}
				// No more client traffic: expiry or the original authority must retire it.
				if _, _, err = ws.Read(ctx); err == nil {
					t.Fatal("invalid connection stayed open")
				}
				deadline := time.Now().Add(time.Second)
				for rt.OnlineConnections(42) != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if rt.OnlineConnections(42) != 0 {
					t.Fatal("business lease not ended")
				}
			})
		}
	}
}

func TestOriginalTenantScopeAndAdmissionRemainEnforced(t *testing.T) {
	i := &originalIssuer{key: []byte("original-key")}
	var resolved atomic.Int32
	a := &Adapter{BackendID: "issuer", Tenant: "a", RequiredScope: "required", Mode: proxy.AuthCheckExpiryOnly, Tokens: i, Resolve: func(context.Context, string) (int64, error) { resolved.Add(1); return 42, nil }}
	for _, v := range []VerifiedToken{{Account: "uuid", Tenant: "b", Scopes: []string{"required"}, ExpiresAt: time.Now().Add(time.Minute)}, {Account: "uuid", Tenant: "a", Scopes: []string{"other"}, ExpiresAt: time.Now().Add(time.Minute)}} {
		if _, err := a.Authenticate(context.Background(), proxy.AuthRequest{Token: i.sign(t, v)}); err == nil {
			t.Fatal("token restriction lost")
		}
	}
	if resolved.Load() != 0 {
		t.Fatal("mapped UID before authorization")
	}
	if _, err := a.Authenticate(context.Background(), proxy.AuthRequest{Token: "forged"}); err == nil {
		t.Fatal("original signature ignored")
	}
}

var _ proxy.Authenticator = (*Adapter)(nil)
var _ proxy.AuthRevalidator = (*Adapter)(nil)
