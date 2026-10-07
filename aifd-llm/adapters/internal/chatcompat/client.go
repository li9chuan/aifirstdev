// Package chatcompat implements OpenAI-compatible chat completions adapters.
package chatcompat

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

// Options configures an OpenAI-compatible chat completions client.
type Options struct {
	Provider     string
	APIKey       string
	BaseURL      string
	UserAgent    string
	DefaultModel string
	HTTPClient   *http.Client
	Headers      map[string]string
}

// Client calls an OpenAI-compatible chat completions API.
type Client struct {
	opts Options
	http *http.Client
}

// NewClient creates an OpenAI-compatible client.
func NewClient(opts Options) *Client {
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	return &Client{opts: opts, http: opts.HTTPClient}
}

// Complete performs a non-streaming chat completion request.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	body, err := c.buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, c.opts.Provider, "marshal chat completion request", err)
	}
	httpReq, err := c.newRequest(ctx, data)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, llm.ContextError(c.opts.Provider, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, llm.WrapError(llm.KindProvider, c.opts.Provider, "read chat completion response", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeHTTPError(c.opts.Provider, resp.StatusCode, raw)
	}
	return decodeResponse(raw)
}

// Stream starts a streaming chat completion request.
func (c *Client) Stream(ctx context.Context, req llm.Request) (llm.StreamReader, error) {
	body, err := c.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, c.opts.Provider, "marshal chat completion stream request", err)
	}
	httpReq, err := c.newRequest(ctx, data)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, llm.ContextError(c.opts.Provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, llm.WrapError(llm.KindProvider, c.opts.Provider, "read chat completion stream error", readErr)
		}
		return nil, decodeHTTPError(c.opts.Provider, resp.StatusCode, raw)
	}
	return newStream(c.opts.Provider, resp.Body), nil
}

func (c *Client) buildRequest(req llm.Request, stream bool) (chatRequest, error) {
	if req.Model == "" {
		req.Model = c.opts.DefaultModel
	}
	if err := llm.ValidateRequest(req); err != nil {
		return chatRequest{}, err
	}
	out := chatRequest{
		ProviderOptions: req.ProviderOptions,
		Model:           req.Model,
		Messages:        make([]chatMessage, 0, len(req.Messages)),
		MaxTokens:       omitZero(req.MaxTokens),
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stop:            req.Stop,
		Stream:          stream,
	}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.JSONMode {
		out.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	if req.ToolChoice != "" {
		out.ToolChoice = req.ToolChoice
	}
	for _, msg := range req.Messages {
		out.Messages = append(out.Messages, encodeMessage(msg))
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, encodeTool(tool))
	}
	return out, nil
}

func (c *Client) newRequest(ctx context.Context, data []byte) (*http.Request, error) {
	endpoint := strings.TrimRight(c.opts.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, llm.WrapError(llm.KindInvalidRequest, c.opts.Provider, "create chat completion request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.opts.UserAgent != "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}
	if c.opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	for key, value := range c.opts.Headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func decodeResponse(raw []byte) (*llm.Response, error) {
	var in chatResponse
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, llm.WrapError(llm.KindProvider, "", "decode chat completion response", err)
	}
	out := &llm.Response{
		ID:    in.ID,
		Model: in.Model,
		Usage: llm.Usage{
			PromptTokens:     in.Usage.PromptTokens,
			CompletionTokens: in.Usage.CompletionTokens,
			TotalTokens:      in.Usage.TotalTokens,
		},
		Raw: append([]byte(nil), raw...),
	}
	for _, choice := range in.Choices {
		out.Choices = append(out.Choices, llm.Choice{
			Index:        choice.Index,
			Message:      decodeMessage(choice.Message),
			FinishReason: choice.FinishReason,
		})
	}
	return out, nil
}

func decodeHTTPError(provider string, status int, raw []byte) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Error.Message != "" {
		code := body.Error.Code
		if code == "" {
			code = body.Error.Type
		}
		return llm.ProviderHTTPError(provider, status, code, body.Error.Message)
	}
	return llm.ProviderHTTPError(provider, status, "", fmt.Sprintf("provider returned %s", strings.TrimSpace(string(raw))))
}

func omitZero(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
