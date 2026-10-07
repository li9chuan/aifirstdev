package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

func TestDefaultModelIsCurrentDeepSeekModel(t *testing.T) {
	var model string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		model, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"id":"d1","model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"think"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(Options{APIKey: "test-key", BaseURL: server.URL})
	rsp, err := client.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if model != defaultModel {
		t.Fatalf("model = %q, want %q", model, defaultModel)
	}
	if got := rsp.FirstMessage().Reasoning; got != "think" {
		t.Fatalf("reasoning = %q", got)
	}
}
