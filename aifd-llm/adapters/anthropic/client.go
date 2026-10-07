// Package anthropic provides an adapter for Anthropic's Messages API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

const (
	defaultBaseURL         = "https://api.anthropic.com/v1"
	defaultUserAgent       = "aifd-llm/anthropic"
	defaultVersion         = "2023-06-01"
	defaultMaxTokens       = 1024
	provider               = "anthropic"
	headerAPIKey           = "x-api-key"
	headerAnthropicVersion = "anthropic-version"
)

// Options configures an Anthropic adapter.
type Options struct {
	APIKey           string
	BaseURL          string
	UserAgent        string
	Version          string
	DefaultModel     string
	DefaultMaxTokens int
	HTTPClient       *http.Client
	Headers          map[string]string
}

// Client calls Anthropic through aifd-llm contracts.
type Client struct {
	opts Options
	http *http.Client
}

// NewClient creates an Anthropic adapter. MaxTokens defaults because the
// Messages API requires it.
func NewClient(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = defaultBaseURL
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	if opts.Version == "" {
		opts.Version = defaultVersion
	}
	if opts.DefaultMaxTokens <= 0 {
		opts.DefaultMaxTokens = defaultMaxTokens
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	return &Client{opts: opts, http: opts.HTTPClient}
}

// Complete performs a non-streaming Messages API request.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	body, err := c.buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, provider, "marshal anthropic request", err)
	}
	httpReq, err := c.newRequest(ctx, data)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, llm.ContextError(provider, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, llm.WrapError(llm.KindProvider, provider, "read anthropic response", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeHTTPError(resp.StatusCode, raw)
	}
	return decodeResponse(raw)
}

// Stream starts a streaming Messages API request.
func (c *Client) Stream(ctx context.Context, req llm.Request) (llm.StreamReader, error) {
	body, err := c.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, provider, "marshal anthropic stream request", err)
	}
	httpReq, err := c.newRequest(ctx, data)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, llm.ContextError(provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, llm.WrapError(llm.KindProvider, provider, "read anthropic stream error", readErr)
		}
		return nil, decodeHTTPError(resp.StatusCode, raw)
	}
	return &stream{reader: llm.NewSSEReader(resp.Body)}, nil
}

func (c *Client) buildRequest(req llm.Request, stream bool) (messageRequest, error) {
	if req.Model == "" {
		req.Model = c.opts.DefaultModel
	}
	if err := llm.ValidateRequest(req); err != nil {
		return messageRequest{}, err
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.opts.DefaultMaxTokens
	}
	out := messageRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
		Stream:      stream,
	}
	for _, msg := range req.Messages {
		if msg.Role == llm.RoleSystem {
			if out.System != "" {
				out.System += "\n"
			}
			out.System += msg.Content
			continue
		}
		out.Messages = append(out.Messages, encodeMessage(msg))
	}
	if req.JSONMode {
		if out.System != "" {
			out.System += "\n"
		}
		out.System += "Return only valid JSON."
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, encodeTool(tool))
	}
	if req.ToolChoice != "" {
		out.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	if len(out.Messages) == 0 {
		return messageRequest{}, llm.NewError(llm.KindInvalidRequest, provider, "anthropic request has no non-system messages")
	}
	return out, nil
}

func (c *Client) newRequest(ctx context.Context, data []byte) (*http.Request, error) {
	endpoint := strings.TrimRight(c.opts.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, provider, "create anthropic request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set(headerAnthropicVersion, c.opts.Version)
	if c.opts.APIKey != "" {
		req.Header.Set(headerAPIKey, c.opts.APIKey)
	}
	for key, value := range c.opts.Headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func decodeHTTPError(status int, raw []byte) error {
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Error.Message != "" {
		return llm.ProviderHTTPError(provider, status, body.Error.Type, body.Error.Message)
	}
	return llm.ProviderHTTPError(provider, status, "", fmt.Sprintf("provider returned %s", strings.TrimSpace(string(raw))))
}
