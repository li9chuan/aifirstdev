package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

func TestCompleteMapsMessagesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get(headerAPIKey); got != "test-key" {
			t.Fatalf("api key = %q", got)
		}
		if got := r.Header.Get(headerAnthropicVersion); got != defaultVersion {
			t.Fatalf("version = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["model"] != "claude-test" || body["max_tokens"].(float64) != defaultMaxTokens {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"id":"m1","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer server.Close()

	client := NewClient(Options{APIKey: "test-key", BaseURL: server.URL})
	rsp, err := client.Complete(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if rsp.Text() != "ok" || rsp.Usage.TotalTokens != 5 {
		t.Fatalf("rsp = %+v", rsp)
	}
}
