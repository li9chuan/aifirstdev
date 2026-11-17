package wechat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

type SubjectKind uint8

const (
	SubjectUnspecified SubjectKind = iota
	SubjectOpenID
	SubjectUnionID
)

// AuthorityID must describe the verified identity space: app-specific for
// OpenID, shared only for apps bound to the same WeChat platform with UnionID.
type Options struct {
	ConnectorID, AuthorityID, AppID, AppSecret string
	SubjectKind                                SubjectKind
}
type MiniProgramProof struct {
	Code string `json:"code"`
}
type WebsiteProof struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	State       string `json:"state"`
}
type WebsiteAuthorizationRequest struct {
	Binding    credential.ChallengeBinding
	RedirectID string
}
type WebsiteChallenge struct {
	ID, AuthorizationURL string
	ExpiresAt            time.Time
}

func EncodeMiniProgramProof(connector string, proof MiniProgramProof) credential.Credential {
	raw, _ := json.Marshal(proof)
	return credential.Credential{ConnectorID: connector, Proof: raw}
}
func EncodeWebsiteProof(connector string, proof WebsiteProof) credential.Credential {
	raw, _ := json.Marshal(proof)
	return credential.Credential{ConnectorID: connector, Proof: raw}
}

type MiniProgramProvider struct {
	client Client
	opts   Options
}

func NewMiniProgramProvider(client Client, opts Options) (*MiniProgramProvider, error) {
	if err := validateOptions(client, opts); err != nil {
		return nil, err
	}
	return &MiniProgramProvider{client: client, opts: opts}, nil
}

// Verify performs direct code exchange. Binding is deliberately not represented
// as code provenance; sensitive account operations authenticate the actor again.
func (p *MiniProgramProvider) Verify(ctx context.Context, req credential.VerifyRequest) (credential.Principal, error) {
	var proof MiniProgramProof
	if req.Credential.ConnectorID != p.opts.ConnectorID || decodeProof(req.Credential.Proof, &proof) != nil || !validCode(proof.Code) {
		return credential.Principal{}, credential.ErrInvalidCredential
	}
	identity, err := p.client.ExchangeMiniProgramCode(ctx, p.opts.AppID, p.opts.AppSecret, proof.Code)
	if err != nil {
		return credential.Principal{}, err
	}
	return principal(p.opts, identity)
}

// AuthorizationRecord carries no phone, OpenID, token, or AppSecret. Store
// implementations must atomically consume the exact state and binding once.
type AuthorizationRecord struct {
	ID          string
	StateDigest [32]byte
	Binding     credential.ChallengeBinding
	ExpiresAt   time.Time
}
type WebsiteStore interface {
	Create(context.Context, AuthorizationRecord, time.Time) error
	Consume(context.Context, string, [32]byte, credential.ChallengeBinding, time.Time) error
}
type WebsiteOptions struct {
	Options
	Redirects map[string]string
	TTL       time.Duration
	Now       func() time.Time
}
type WebsiteProvider struct {
	client Client
	store  WebsiteStore
	opts   WebsiteOptions
}

func NewWebsiteProvider(client Client, store WebsiteStore, opts WebsiteOptions) (*WebsiteProvider, error) {
	if err := validateOptions(client, opts.Options); err != nil {
		return nil, err
	}
	if store == nil || len(opts.Redirects) == 0 {
		return nil, fmt.Errorf("website authorization store and redirects required")
	}
	redirects := make(map[string]string, len(opts.Redirects))
	for id, value := range opts.Redirects {
		u, err := url.Parse(value)
		if err != nil || !credential.ValidID(id) || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("invalid configured website redirect")
		}
		redirects[id] = value
	}
	opts.Redirects = redirects
	if opts.TTL == 0 {
		opts.TTL = 5 * time.Minute
	}
	if opts.TTL < time.Second || opts.TTL > 10*time.Minute {
		return nil, fmt.Errorf("invalid website challenge lifetime")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &WebsiteProvider{client: client, store: store, opts: opts}, nil
}

func (p *WebsiteProvider) BeginAuthorization(ctx context.Context, req WebsiteAuthorizationRequest) (WebsiteChallenge, error) {
	redirect, ok := p.opts.Redirects[req.RedirectID]
	if !ok || !validBinding(req.Binding) {
		return WebsiteChallenge{}, credential.ErrInvalidCredential
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return WebsiteChallenge{}, credential.ErrUnavailable
	}
	id, state := hex.EncodeToString(random[:16]), hex.EncodeToString(random[16:])
	now := p.opts.Now()
	expires := now.Add(p.opts.TTL)
	if err := p.store.Create(ctx, AuthorizationRecord{ID: p.opts.ConnectorID + ":" + id, StateDigest: sha256.Sum256([]byte(state)), Binding: req.Binding, ExpiresAt: expires}, now); err != nil {
		return WebsiteChallenge{}, err
	}
	q := url.Values{"appid": {p.opts.AppID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"snsapi_login"}, "state": {state}}
	return WebsiteChallenge{ID: id, AuthorizationURL: "https://open.weixin.qq.com/connect/qrconnect?" + q.Encode() + "#wechat_redirect", ExpiresAt: expires}, nil
}

// Verify consumes the challenge before exchange. An uncertain upstream exchange
// requires a new authorization flow; it never replays a potentially used code.
func (p *WebsiteProvider) Verify(ctx context.Context, req credential.VerifyRequest) (credential.Principal, error) {
	var proof WebsiteProof
	if req.Credential.ConnectorID != p.opts.ConnectorID || !validBinding(req.Binding) || decodeProof(req.Credential.Proof, &proof) != nil || len(proof.ChallengeID) != 32 || len(proof.State) != 32 || !validCode(proof.Code) {
		return credential.Principal{}, credential.ErrInvalidCredential
	}
	if err := p.store.Consume(ctx, p.opts.ConnectorID+":"+proof.ChallengeID, sha256.Sum256([]byte(proof.State)), req.Binding, p.opts.Now()); err != nil {
		return credential.Principal{}, err
	}
	identity, err := p.client.ExchangeWebsiteCode(ctx, p.opts.AppID, p.opts.AppSecret, proof.Code)
	if err != nil {
		return credential.Principal{}, err
	}
	return principal(p.opts.Options, identity)
}

func validateOptions(client Client, o Options) error {
	if client == nil || !credential.ValidID(o.ConnectorID) || !credential.ValidID(o.AuthorityID) || !credential.ValidID(o.AppID) || o.AppSecret == "" || o.SubjectKind != SubjectOpenID && o.SubjectKind != SubjectUnionID {
		return fmt.Errorf("invalid WeChat verifier configuration")
	}
	return nil
}
func principal(o Options, i Identity) (credential.Principal, error) {
	subject := i.OpenID
	if o.SubjectKind == SubjectUnionID {
		subject = i.UnionID
	}
	if subject == "" || len(subject) > 256 {
		return credential.Principal{}, credential.ErrInvalidCredential
	}
	return credential.Principal{AuthorityID: o.AuthorityID, Subject: subject}, nil
}
func decodeProof(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > credential.MaxProofBytes {
		return credential.ErrInvalidCredential
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return credential.ErrInvalidCredential
	}
	return nil
}
func validBinding(b credential.ChallengeBinding) bool {
	return credential.ValidID(b.Purpose) && b.ScopeDigest != ([32]byte{})
}
func validCode(s string) bool { return len(s) > 0 && len(s) <= 2048 && strings.TrimSpace(s) == s }
