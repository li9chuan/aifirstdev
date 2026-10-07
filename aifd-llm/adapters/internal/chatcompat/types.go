package chatcompat

import (
	"encoding/json"
	"fmt"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

type chatRequest struct {
	ProviderOptions map[string]any  `json:"-"`
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	MaxTokens       *int            `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	StreamOptions   *streamOptions  `json:"stream_options,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
	Tools           []chatTool      `json:"tools,omitempty"`
	ToolChoice      string          `json:"tool_choice,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	Name             string         `json:"name,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatToolFunction `json:"function,omitempty"`
}

type chatToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func encodeMessage(msg llm.Message) chatMessage {
	out := chatMessage{
		Role:             string(msg.Role),
		Content:          msg.Content,
		Name:             msg.Name,
		ToolCallID:       msg.ToolCallID,
		ReasoningContent: msg.Reasoning,
	}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, encodeToolCall(call))
	}
	return out
}

func decodeMessage(msg chatMessage) llm.Message {
	out := llm.Message{
		Role:       llm.Role(msg.Role),
		Content:    msg.Content,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
		Reasoning:  msg.ReasoningContent,
	}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, decodeToolCall(call))
	}
	return out
}

func encodeTool(tool llm.Tool) chatTool {
	params := tool.Parameters
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return chatTool{
		Type: "function",
		Function: chatFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  params,
		},
	}
}

func encodeToolCall(call llm.ToolCall) chatToolCall {
	typ := call.Type
	if typ == "" {
		typ = "function"
	}
	return chatToolCall{
		ID:   call.ID,
		Type: typ,
		Function: chatToolFunction{
			Name:      call.Name,
			Arguments: call.Arguments,
		},
	}
}

func decodeToolCall(call chatToolCall) llm.ToolCall {
	return llm.ToolCall{
		ID:        call.ID,
		Type:      call.Type,
		Name:      call.Function.Name,
		Arguments: call.Function.Arguments,
	}
}

// MarshalJSON preserves provider extensions without allowing them to replace
// provider-neutral request fields, including fields omitted in this request.
func (r chatRequest) MarshalJSON() ([]byte, error) {
	type requestFields chatRequest
	raw, err := json.Marshal(requestFields(r))
	if err != nil {
		return nil, err
	}
	if len(r.ProviderOptions) == 0 {
		return raw, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for name, value := range r.ProviderOptions {
		switch name {
		case "", "model", "messages", "max_tokens", "temperature", "top_p", "stop", "stream", "stream_options", "response_format", "tools", "tool_choice":
			return nil, fmt.Errorf("reserved provider option: %s", name)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body[name] = encoded
	}
	return json.Marshal(body)
}
