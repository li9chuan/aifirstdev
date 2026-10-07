package app

import (
	"context"
	"testing"
)

func TestCounterConfigReloadRejectsInvalidLimit(t *testing.T) {
	app, err := NewConfigApp(context.Background())
	if err != nil {
		t.Fatalf("NewConfigApp error = %v", err)
	}
	if app.CounterLimit() != defaultCounterLimit {
		t.Fatalf("default limit = %d, want %d", app.CounterLimit(), defaultCounterLimit)
	}
	if err := app.SetCounterLimit(context.Background(), 0); err == nil {
		t.Fatal("SetCounterLimit invalid error = nil")
	}
	if app.CounterLimit() != defaultCounterLimit {
		t.Fatalf("limit after rejected reload = %d, want %d", app.CounterLimit(), defaultCounterLimit)
	}
}
