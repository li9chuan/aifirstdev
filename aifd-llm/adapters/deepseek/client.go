// Package deepseek provides an adapter for DeepSeek's OpenAI-compatible API.
package deepseek

import (
	"context"
	"net/http"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
	"github.com/li9chuan/aifirstdev/aifd-llm/adapters/internal/chatcompat"
)

const (
	defaultBaseURL      = "https://api.deepseek.com"
	defaultUserAgent    = "aifd-llm/deepseek"
	defaultModel        = "deepseek-v4-flash"
	betaBaseURL         = "https://api.deepseek.com/beta"
	headerBetaPrefixAPI = "deepseek-prefix"
)

// Options configures a DeepSeek adapter.
type Options struct {
	APIKey        string
	BaseURL       string
	UserAgent     string
	DefaultModel  string
	HTTPClient    *http.Client
	Headers       map[string]string
	BetaPrefixAPI bool
}

// Client calls DeepSeek through aifd-llm contracts.
type Client struct {
	inner *chatcompat.Client
}

// NewClient creates a DeepSeek adapter. The default model avoids the old
// deepseek-chat alias used by legacy code.
func NewClient(opts Options) *Client {
	if opts.BaseURL == "" {
		if opts.BetaPrefixAPI {
			opts.BaseURL = betaBaseURL
		} else {
			opts.BaseURL = defaultBaseURL
		}
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	if opts.DefaultModel == "" {
		opts.DefaultModel = defaultModel
	}
	headers := make(map[string]string, len(opts.Headers)+1)
	for key, value := range opts.Headers {
		headers[key] = value
	}
	if opts.BetaPrefixAPI {
		headers[headerBetaPrefixAPI] = "true"
	}
	return &Client{inner: chatcompat.NewClient(chatcompat.Options{
		Provider:     "deepseek",
		APIKey:       opts.APIKey,
		BaseURL:      opts.BaseURL,
		UserAgent:    opts.UserAgent,
		DefaultModel: opts.DefaultModel,
		HTTPClient:   opts.HTTPClient,
		Headers:      headers,
	})}
}

// Complete performs a non-streaming model call.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return c.inner.Complete(ctx, req)
}

// Stream starts a streaming model call.
func (c *Client) Stream(ctx context.Context, req llm.Request) (llm.StreamReader, error) {
	return c.inner.Stream(ctx, req)
}
