package llm

import (
	"io"
	"strings"
	"testing"
)

func TestSSEReader(t *testing.T) {
	reader := NewSSEReader(io.NopCloser(strings.NewReader("event: message\ndata: {\"a\":1}\ndata: {\"b\":2}\n\n")))
	event, err := reader.Read()
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	if event.Event != "message" || string(event.Data) != "{\"a\":1}\n{\"b\":2}" {
		t.Fatalf("event = %+v", event)
	}
}
