package fake

import (
	"context"
	"testing"

	llm "github.com/li9chuan/aifirstdev/aifd-llm"
)

func TestFakeClientRecordsCalls(t *testing.T) {
	client := NewClient(Result{Response: &llm.Response{
		Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}},
	}})
	rsp, err := client.Complete(context.Background(), llm.Request{
		Model:    "fake",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if rsp.Text() != "ok" {
		t.Fatalf("text = %q", rsp.Text())
	}
	if got := len(client.Calls()); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}
