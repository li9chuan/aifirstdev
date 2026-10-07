// Package fake provides an in-memory llm.Client for tests and local development.
package fake

import (
	"context"
	"io"
	"sync"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

// Result is a queued non-streaming fake result.
type Result struct {
	Response *llm.Response
	Err      error
}

// StreamResult is a queued streaming fake result.
type StreamResult struct {
	Events []llm.StreamEvent
	Err    error
}

// Client records calls and returns queued fake results.
type Client struct {
	mu       sync.Mutex
	calls    []llm.Request
	results  []Result
	streams  []StreamResult
	fallback string
}

// NewClient creates a fake client.
func NewClient(results ...Result) *Client {
	return &Client{results: append([]Result(nil), results...)}
}

// WithFallbackText sets the assistant text returned when no result is queued.
func (c *Client) WithFallbackText(text string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fallback = text
	return c
}

// Queue appends a non-streaming fake result.
func (c *Client) Queue(result Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, result)
}

// QueueStream appends a streaming fake result.
func (c *Client) QueueStream(result StreamResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streams = append(c.streams, result)
}

// Calls returns a copy of recorded requests.
func (c *Client) Calls() []llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]llm.Request(nil), c.calls...)
}

// Complete records req and returns the next queued result or a fallback response.
func (c *Client) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, req)
	if len(c.results) > 0 {
		result := c.results[0]
		c.results = c.results[1:]
		return result.Response, result.Err
	}
	return &llm.Response{
		Model: req.Model,
		Choices: []llm.Choice{{
			Index: 0,
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: c.fallback,
			},
			FinishReason: "stop",
		}},
	}, nil
}

// Stream records req and returns the next queued stream.
func (c *Client) Stream(_ context.Context, req llm.Request) (llm.StreamReader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, req)
	if len(c.streams) == 0 {
		return &stream{events: []llm.StreamEvent{{Type: llm.StreamEventDone}}}, nil
	}
	result := c.streams[0]
	c.streams = c.streams[1:]
	if result.Err != nil {
		return nil, result.Err
	}
	return &stream{events: append([]llm.StreamEvent(nil), result.Events...)}, nil
}

type stream struct {
	events []llm.StreamEvent
}

func (s *stream) Recv() (llm.StreamEvent, error) {
	if len(s.events) == 0 {
		return llm.StreamEvent{}, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *stream) Close() error {
	s.events = nil
	return nil
}
