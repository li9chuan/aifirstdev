package chatcompat

import (
	"encoding/json"
	"io"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

type stream struct {
	provider string
	reader   *llm.SSEReader
}

func newStream(provider string, body io.ReadCloser) llm.StreamReader {
	return &stream{provider: provider, reader: llm.NewSSEReader(body)}
}

func (s *stream) Recv() (llm.StreamEvent, error) {
	for {
		event, err := s.reader.Read()
		if err != nil {
			return llm.StreamEvent{}, err
		}
		if string(event.Data) == "[DONE]" {
			return llm.StreamEvent{Type: llm.StreamEventDone}, nil
		}
		var chunk chatChunk
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			return llm.StreamEvent{
				Type: llm.StreamEventError,
				Raw:  append([]byte(nil), event.Data...),
				Err:  llm.WrapError(llm.KindProvider, s.provider, "decode chat completion stream chunk", err),
			}, nil
		}
		if chunk.Usage != nil {
			return llm.StreamEvent{
				Type: llm.StreamEventUsage,
				Usage: llm.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				},
				Raw: append([]byte(nil), event.Data...),
			}, nil
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if len(choice.Delta.ToolCalls) > 0 {
			call := decodeToolCall(choice.Delta.ToolCalls[0])
			return llm.StreamEvent{Type: llm.StreamEventToolCall, ToolCall: &call, Raw: append([]byte(nil), event.Data...)}, nil
		}
		if choice.FinishReason != "" {
			return llm.StreamEvent{Type: llm.StreamEventDone, FinishReason: choice.FinishReason, Raw: append([]byte(nil), event.Data...)}, nil
		}
		if choice.Delta.Content == "" && choice.Delta.ReasoningContent == "" {
			continue
		}
		return llm.StreamEvent{
			Type:           llm.StreamEventDelta,
			Delta:          choice.Delta.Content,
			ReasoningDelta: choice.Delta.ReasoningContent,
			Raw:            append([]byte(nil), event.Data...),
		}, nil
	}
}

func (s *stream) Close() error {
	if s == nil || s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

type chatChunk struct {
	Choices []struct {
		Delta        chatMessage `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}
