// Package wechat verifies official website and mini-program login codes without
// depending on an account store, account IDs, or AIFD tickets.
package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

type Identity struct{ OpenID, UnionID string }

// Client exchanges codes with the authority. Implementations must verify against
// the configured app and must never trust identity fields supplied by a client.
type Client interface {
	ExchangeWebsiteCode(context.Context, string, string, string) (Identity, error)
	ExchangeMiniProgramCode(context.Context, string, string, string) (Identity, error)
}

type HTTPClient struct{ client *http.Client }

// NewHTTPClient copies the client, bounds requests and rejects redirects so an
// upstream redirect cannot forward AppSecret/code to another origin.
func NewHTTPClient(client *http.Client) *HTTPClient {
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	// Code exchange consumes a one-time credential. A reused HTTP connection
	// may silently replay GET after a lost response, so own a non-reusing
	// HTTP/1 transport. Custom RoundTrippers must provide the same no-replay rule.
	base, ok := c.Transport.(*http.Transport)
	if c.Transport == nil {
		base, ok = http.DefaultTransport.(*http.Transport)
	}
	if ok {
		transport := base.Clone()
		transport.DisableKeepAlives = true
		transport.ForceAttemptHTTP2 = false
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
		c.Transport = transport
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPClient{client: &c}
}
func (c *HTTPClient) ExchangeWebsiteCode(ctx context.Context, appID, secret, code string) (Identity, error) {
	return c.exchange(ctx, "/sns/oauth2/access_token", appID, secret, code, false)
}
func (c *HTTPClient) ExchangeMiniProgramCode(ctx context.Context, appID, secret, code string) (Identity, error) {
	return c.exchange(ctx, "/sns/jscode2session", appID, secret, code, true)
}

func (c *HTTPClient) exchange(ctx context.Context, path, appID, secret, code string, mini bool) (Identity, error) {
	q := url.Values{"appid": {appID}, "secret": {secret}, "grant_type": {"authorization_code"}}
	if mini {
		q.Set("js_code", code)
	} else {
		q.Set("code", code)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com"+path+"?"+q.Encode(), nil)
	if err != nil {
		return Identity{}, credential.ErrUnavailable
	}
	rsp, err := c.client.Do(req)
	if err != nil {
		return Identity{}, credential.ErrUnavailable
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return Identity{}, credential.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(rsp.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return Identity{}, credential.ErrUnavailable
	}
	var data struct {
		OpenID      string `json:"openid"`
		UnionID     string `json:"unionid"`
		SessionKey  string `json:"session_key"`
		AccessToken string `json:"access_token"`
		ErrorCode   int    `json:"errcode"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return Identity{}, credential.ErrUnavailable
	}
	if data.ErrorCode == -1 || data.ErrorCode == 45011 {
		return Identity{}, credential.ErrUnavailable
	}
	if data.ErrorCode != 0 || data.OpenID == "" || len(data.OpenID) > 256 || len(data.UnionID) > 256 || mini && data.SessionKey == "" || !mini && data.AccessToken == "" {
		return Identity{}, credential.ErrInvalidCredential
	}
	return Identity{OpenID: data.OpenID, UnionID: data.UnionID}, nil
}
