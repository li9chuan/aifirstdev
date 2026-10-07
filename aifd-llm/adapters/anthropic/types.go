package anthropic

import (
	"encoding/json"
	"strings"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

type messageRequest struct {
	Model       string         `json:"model"`
	MaxTokens   int            `json:"max_tokens"`
	System      string         `json:"system,omitempty"`
	Messages    []message      `json:"messages"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	Stop        []string       `json:"stop_sequences,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Tools       []tool         `json:"tools,omitempty"`
	ToolChoice  map[string]any `json:"tool_choice,omitempty"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type messageResponse struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func encodeMessage(msg llm.Message) message {
	role := string(msg.Role)
	if msg.Role == llm.RoleTool {
		return message{
			Role: "user",
			Content: []contentBlock{{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			}},
		}
	}
	if role == "" {
		role = "user"
	}
	blocks := make([]contentBlock, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: msg.Content})
	}
	for _, call := range msg.ToolCalls {
		input := json.RawMessage(call.Arguments)
		if !json.Valid(input) {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, contentBlock{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Name,
			Input: input,
		})
	}
	return message{Role: role, Content: blocks}
}

func encodeTool(t llm.Tool) tool {
	schema := t.Parameters
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return tool{Name: t.Name, Description: t.Description, InputSchema: schema}
}

func encodeToolChoice(choice string) map[string]any {
	switch choice {
	case "auto", "any", "none":
		return map[string]any{"type": choice}
	default:
		return map[string]any{"type": "tool", "name": choice}
	}
}

func decodeResponse(raw []byte) (*llm.Response, error) {
	var in messageResponse
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, llm.WrapError(llm.KindProvider, provider, "decode anthropic response", err)
	}
	var text strings.Builder
	var calls []llm.ToolCall
	for _, block := range in.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			calls = append(calls, llm.ToolCall{
				ID:        block.ID,
				Type:      "function",
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}
	return &llm.Response{
		ID:    in.ID,
		Model: in.Model,
		Choices: []llm.Choice{{
			Index: 0,
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				Content:   text.String(),
				ToolCalls: calls,
			},
			FinishReason: in.StopReason,
		}},
		Usage: llm.Usage{
			PromptTokens:     in.Usage.InputTokens,
			CompletionTokens: in.Usage.OutputTokens,
			TotalTokens:      in.Usage.InputTokens + in.Usage.OutputTokens,
		},
		Raw: append([]byte(nil), raw...),
	}, nil
}
