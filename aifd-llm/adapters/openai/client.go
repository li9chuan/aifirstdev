// Package openai provides an adapter for OpenAI-compatible chat completions.
package openai

import (
	"context"
	"net/http"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
	"github.com/li9chuan/aifirstdev/aifd-llm/adapters/internal/chatcompat"
)

const (
	defaultBaseURL   = "https://api.openai.com/v1"
	defaultUserAgent = "aifd-llm/openai"
)

// Options configures an OpenAI adapter.
type Options struct {
	APIKey       string
	BaseURL      string
	UserAgent    string
	DefaultModel string
	HTTPClient   *http.Client
	Headers      map[string]string
}

// Client calls OpenAI through aifd-llm contracts.
type Client struct {
	inner *chatcompat.Client
}

// NewClient creates an OpenAI adapter. Model is intentionally request-owned
// unless DefaultModel is configured.
func NewClient(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = defaultBaseURL
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	return &Client{inner: chatcompat.NewClient(chatcompat.Options{
		Provider:     "openai",
		APIKey:       opts.APIKey,
		BaseURL:      opts.BaseURL,
		UserAgent:    opts.UserAgent,
		DefaultModel: opts.DefaultModel,
		HTTPClient:   opts.HTTPClient,
		Headers:      opts.Headers,
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
