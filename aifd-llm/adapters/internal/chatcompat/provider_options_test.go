package chatcompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

func TestProviderOptionsReachCompleteAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["thinking"]) != `{"type":"disabled"}` || string(body["reasoning_effort"]) != `"low"` {
				t.Error("provider options lost")
			}
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return
			}
			_, _ = w.Write([]byte(`{"id":"test","model":"test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(server.Close)
		client := NewClient(Options{Provider: "test", BaseURL: server.URL})
		req := llm.Request{Model: "test", Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}, ProviderOptions: map[string]any{"thinking": map[string]string{"type": "disabled"}, "reasoning_effort": "low"}}
		if stream {
			reader, err := client.Stream(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			_ = reader.Close()
		} else {
			if _, err := client.Complete(context.Background(), req); err != nil {
				t.Fatal(err)
			}
		}
		server.Close()
	}
}

func TestInvalidProviderOptionsFailBeforeHTTP(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	client := NewClient(Options{Provider: "test", BaseURL: server.URL})
	for _, stream := range []bool{false, true} {
		req := llm.Request{Model: "test", Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}, ProviderOptions: map[string]any{"model": "override"}}
		var err error
		if stream {
			_, err = client.Stream(context.Background(), req)
		} else {
			_, err = client.Complete(context.Background(), req)
		}
		if !llm.IsKind(err, llm.KindInvalidRequest) || called {
			t.Fatal("invalid options were sent or misclassified", err)
		}
	}
}

func TestProviderOptionsCannotOverrideCommonFields(t *testing.T) {
	for _, key := range []string{"", "model", "messages", "max_tokens", "temperature", "top_p", "stop", "stream", "stream_options", "response_format", "tools", "tool_choice"} {
		if _, err := json.Marshal(chatRequest{ProviderOptions: map[string]any{key: "override"}}); err == nil {
			t.Fatal("reserved option accepted", key)
		}
	}
	if _, err := json.Marshal(chatRequest{ProviderOptions: map[string]any{"thinking": make(chan int)}}); err == nil {
		t.Fatal("non JSON option accepted")
	}
}
