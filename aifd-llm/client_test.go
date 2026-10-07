package llm

import "testing"

func TestValidateRequest(t *testing.T) {
	err := ValidateRequest(Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("ValidateRequest error = %v", err)
	}
	if err := ValidateRequest(Request{}); !IsKind(err, KindInvalidRequest) {
		t.Fatalf("ValidateRequest empty err=%v, want invalid request", err)
	}
}
