package proxy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	proxyc "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

func TestLoginHandlerDefaultsUseAuthTopics(t *testing.T) {
	_, verifier := newTestTokenTools(t)
	handler := proxyc.NewLoginHandler(Authenticate(nil, verifier))
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
	if handler.Topic() != auth.TopicMceAuth || handler.ResultTopic() != auth.TopicMseAuth {
		t.Fatalf("topics = %q/%q, want MceAuth/MseAuth", handler.Topic(), handler.ResultTopic())
	}
}

func TestLoginHandlerInvalidTokenReturnsMseAuth(t *testing.T) {
	_, verifier := newTestTokenTools(t)
	msg, err := rpc.NewMessagePB(auth.TopicMceAuth, &pbmsg.MceAuth{
		Payload: testPayload(),
		Token:   "bad",
	})
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	handler := proxyc.NewLoginHandler(Authenticate(nil, verifier))
	result, err := handler.Login(context.Background(), loginRequest(msg))
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	rsp := decodeLoginResponse(t, result)
	if rsp.GetRet() != auth.RetInvalidAuth || !result.CloseAfterResponse {
		t.Fatalf("result = %+v rsp=%+v, want invalid token close", result, rsp)
	}
}

func TestLoginHandlerRejectsTamperedPayload(t *testing.T) {
	signer, verifier := newTestTokenTools(t)
	req := &pbmsg.MceAuth{Payload: testPayload()}
	payloadBytes, err := auth.CanonicalPayloadBytes(req.GetPayload())
	if err != nil {
		t.Fatalf("CanonicalPayloadBytes error = %v", err)
	}
	req.Token, err = signer.Sign(payloadBytes, token.SignOptions{ExpiresAt: time.Unix(1100, 0).Unix()})
	if err != nil {
		t.Fatalf("Sign error = %v", err)
	}
	req.Payload.Uid = 2002
	msg, err := rpc.NewMessagePB(auth.TopicMceAuth, req)
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	handler := proxyc.NewLoginHandler(Authenticate(nil, verifier))
	result, err := handler.Login(context.Background(), loginRequest(msg))
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	rsp := decodeLoginResponse(t, result)
	if rsp.GetRet() != auth.RetInvalidAuth || !result.CloseAfterResponse {
		t.Fatalf("result = %+v rsp=%+v, want tampered payload close", result, rsp)
	}
}

func newTestTokenTools(t *testing.T) (*token.Signer, *token.Verifier) {
	t.Helper()
	now := time.Unix(1000, 0)
	key := token.Key{ID: "test", Secret: []byte("secret")}
	signer, err := token.NewSigner(key, token.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}
	verifier, err := token.NewVerifier([]token.Key{key}, token.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewVerifier error = %v", err)
	}
	return signer, verifier
}

func testPayload() *pbmsg.MAuthPayload {
	return &pbmsg.MAuthPayload{
		AccountId:   5001,
		Uid:         1001,
		RealmId:     1,
		Provider:    "demo",
		SubjectHash: strings.Repeat("a", 64),
		TokenId:     "token-1",
		IssuedAt:    time.Unix(1000, 0).Unix(),
		ExpiresAt:   time.Unix(1100, 0).Unix(),
	}
}

func loginRequest(msg rpc.Message) proxyc.LoginRequest {
	return proxyc.LoginRequest{Message: msg}
}

func decodeLoginResponse(t *testing.T, result proxyc.LoginResult) pbmsg.MseAuth {
	t.Helper()
	if result.Response == nil {
		t.Fatal("response = nil")
	}
	var rsp pbmsg.MseAuth
	if err := result.Response.DecodePB(&rsp); err != nil {
		t.Fatalf("DecodePB error = %v", err)
	}
	return rsp
}
