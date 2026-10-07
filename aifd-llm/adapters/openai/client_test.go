package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

func TestCompleteMapsRequestAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("model = %v", body["model"])
		}
		if format, ok := body["response_format"].(map[string]any); !ok || format["type"] != "json_object" {
			t.Fatalf("response_format = %#v", body["response_format"])
		}
		_, _ = w.Write([]byte(`{"id":"c1","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	client := NewClient(Options{APIKey: "test-key", BaseURL: server.URL})
	rsp, err := client.Complete(context.Background(), llm.Request{
		Model:    "gpt-test",
		JSONMode: true,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if rsp.Text() != "ok" || rsp.Usage.TotalTokens != 3 {
		t.Fatalf("rsp = %+v", rsp)
	}
}
