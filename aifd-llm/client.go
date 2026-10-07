// Package llm defines provider-neutral contracts for calling large language
// model APIs from backend services and desktop Go bindings.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Role is the participant role for a chat-style model message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Client calls a model provider through a provider-neutral request surface.
type Client interface {
	Complete(context.Context, Request) (*Response, error)
	Stream(context.Context, Request) (StreamReader, error)
}

// Request describes one model call. Provider-specific tuning belongs in
// ProviderOptions so business code does not import provider SDK types.
type Request struct {
	Model           string
	Messages        []Message
	MaxTokens       int
	Temperature     *float64
	TopP            *float64
	Stop            []string
	JSONMode        bool
	Tools           []Tool
	ToolChoice      string
	Metadata        map[string]string
	ProviderOptions map[string]any
}

// Message is a provider-neutral text or tool message.
type Message struct {
	Role       Role
	Content    string
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
	Reasoning  string
}

// Tool describes a callable tool/function exposed to a model.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is a model request to invoke a tool.
type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// Choice is one candidate model output.
type Choice struct {
	Index        int
	Message      Message
	FinishReason string
}

// Usage reports token accounting returned by a provider.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is a provider-neutral model response.
type Response struct {
	ID      string
	Model   string
	Choices []Choice
	Usage   Usage
	Raw     json.RawMessage
}

// FirstMessage returns the first candidate message, if any.
func (r *Response) FirstMessage() Message {
	if r == nil || len(r.Choices) == 0 {
		return Message{}
	}
	return r.Choices[0].Message
}

// Text returns the first candidate text content, if any.
func (r *Response) Text() string {
	return r.FirstMessage().Content
}

// Float64 returns a pointer suitable for optional float request fields.
func Float64(v float64) *float64 {
	return &v
}

// ValidateRequest checks provider-neutral invariants before adapter mapping.
func ValidateRequest(req Request) error {
	if req.Model == "" {
		return NewError(KindInvalidRequest, "", "llm request model is empty")
	}
	if len(req.Messages) == 0 {
		return NewError(KindInvalidRequest, "", "llm request messages are empty")
	}
	for i, msg := range req.Messages {
		if msg.Role == "" {
			return NewError(KindInvalidRequest, "", fmt.Sprintf("llm message %d role is empty", i))
		}
		if msg.Role != RoleAssistant && msg.Content == "" && msg.ToolCallID == "" {
			return NewError(KindInvalidRequest, "", fmt.Sprintf("llm message %d content is empty", i))
		}
	}
	for i, tool := range req.Tools {
		if tool.Name == "" {
			return NewError(KindInvalidRequest, "", fmt.Sprintf("llm tool %d name is empty", i))
		}
	}
	return nil
}
