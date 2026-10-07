package anthropic

import (
	"encoding/json"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

type stream struct {
	reader *llm.SSEReader
}

func (s *stream) Recv() (llm.StreamEvent, error) {
	for {
		raw, err := s.reader.Read()
		if err != nil {
			return llm.StreamEvent{}, err
		}
		var chunk streamChunk
		if err := json.Unmarshal(raw.Data, &chunk); err != nil {
			return llm.StreamEvent{
				Type: llm.StreamEventError,
				Raw:  append([]byte(nil), raw.Data...),
				Err:  llm.WrapError(llm.KindProvider, provider, "decode anthropic stream chunk", err),
			}, nil
		}
		switch raw.Event {
		case "content_block_delta":
			if chunk.Delta.Text != "" {
				return llm.StreamEvent{Type: llm.StreamEventDelta, Delta: chunk.Delta.Text, Raw: append([]byte(nil), raw.Data...)}, nil
			}
			if chunk.Delta.PartialJSON != "" {
				return llm.StreamEvent{Type: llm.StreamEventToolCall, ToolCall: &llm.ToolCall{Type: "function", Arguments: chunk.Delta.PartialJSON}, Raw: append([]byte(nil), raw.Data...)}, nil
			}
		case "message_delta":
			usage := llm.Usage{CompletionTokens: chunk.Usage.OutputTokens, TotalTokens: chunk.Usage.OutputTokens}
			if chunk.Usage.OutputTokens > 0 {
				return llm.StreamEvent{Type: llm.StreamEventUsage, Usage: usage, Raw: append([]byte(nil), raw.Data...)}, nil
			}
			if chunk.Delta.StopReason != "" {
				return llm.StreamEvent{Type: llm.StreamEventDone, FinishReason: chunk.Delta.StopReason, Raw: append([]byte(nil), raw.Data...)}, nil
			}
		case "message_stop":
			return llm.StreamEvent{Type: llm.StreamEventDone, Raw: append([]byte(nil), raw.Data...)}, nil
		case "error":
			return llm.StreamEvent{Type: llm.StreamEventError, Raw: append([]byte(nil), raw.Data...), Err: llm.NewError(llm.KindProvider, provider, chunk.Error.Message)}, nil
		}
	}
}

func (s *stream) Close() error {
	if s == nil || s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

type streamChunk struct {
	Type  string `json:"type"`
	Delta struct {
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
