package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// StreamEventType identifies a streamed model event.
type StreamEventType string

const (
	StreamEventDelta    StreamEventType = "delta"
	StreamEventToolCall StreamEventType = "tool_call"
	StreamEventUsage    StreamEventType = "usage"
	StreamEventDone     StreamEventType = "done"
	StreamEventError    StreamEventType = "error"
)

// StreamEvent is one provider-neutral event from a streamed response.
type StreamEvent struct {
	Type           StreamEventType
	Delta          string
	ReasoningDelta string
	ToolCall       *ToolCall
	Usage          Usage
	FinishReason   string
	Raw            json.RawMessage
	Err            error
}

// StreamReader receives streamed events from a provider.
type StreamReader interface {
	Recv() (StreamEvent, error)
	Close() error
}

// StreamReaderFunc adapts functions into StreamReader.
type StreamReaderFunc struct {
	RecvFunc  func() (StreamEvent, error)
	CloseFunc func() error
}

// Recv returns the next stream event.
func (s StreamReaderFunc) Recv() (StreamEvent, error) {
	if s.RecvFunc == nil {
		return StreamEvent{}, io.EOF
	}
	return s.RecvFunc()
}

// Close releases stream resources.
func (s StreamReaderFunc) Close() error {
	if s.CloseFunc == nil {
		return nil
	}
	return s.CloseFunc()
}

// SSEEvent is one raw Server-Sent Events frame.
type SSEEvent struct {
	Event string
	Data  []byte
}

// SSEReader parses Server-Sent Events data frames.
type SSEReader struct {
	scanner *bufio.Scanner
	closer  io.Closer
}

// NewSSEReader creates an SSE parser over body.
func NewSSEReader(body io.ReadCloser) *SSEReader {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &SSEReader{scanner: scanner, closer: body}
}

// Read returns the next SSE event. Multiple data lines are joined with '\n'.
func (r *SSEReader) Read() (SSEEvent, error) {
	var event string
	var data [][]byte
	for r.scanner.Scan() {
		line := strings.TrimSuffix(r.scanner.Text(), "\r")
		if line == "" {
			if len(data) == 0 && event == "" {
				continue
			}
			return SSEEvent{Event: event, Data: bytes.Join(data, []byte("\n"))}, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
		}
	}
	if err := r.scanner.Err(); err != nil {
		return SSEEvent{}, err
	}
	if len(data) > 0 || event != "" {
		return SSEEvent{Event: event, Data: bytes.Join(data, []byte("\n"))}, nil
	}
	return SSEEvent{}, io.EOF
}

// Close closes the underlying response body.
func (r *SSEReader) Close() error {
	if r == nil || r.closer == nil {
		return nil
	}
	return r.closer.Close()
}
