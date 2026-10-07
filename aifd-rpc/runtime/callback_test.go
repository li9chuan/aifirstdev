package runtime

import (
	"context"
	"testing"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

func TestCallbackCacheResolveOnce(t *testing.T) {
	cache := NewCallbackCache(time.Second)
	called := 0
	err := cache.Add(CallbackEntry{
		Inbox: "inbox.1",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			called++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	ok, err := cache.Resolve(context.Background(), "inbox.1", NewSession(SessionOptions{}), rpc.Message{Name: "Rsp"})
	if err != nil || !ok {
		t.Fatalf("Resolve ok=%v err=%v", ok, err)
	}
	ok, err = cache.Resolve(context.Background(), "inbox.1", NewSession(SessionOptions{}), rpc.Message{Name: "Rsp"})
	if err != nil || ok {
		t.Fatalf("second Resolve ok=%v err=%v", ok, err)
	}
	if called != 1 {
		t.Fatalf("called = %d, want 1", called)
	}
}

func TestCallbackCacheTimeout(t *testing.T) {
	cache := NewCallbackCache(10 * time.Millisecond)
	defer cache.Close()
	timedOut := make(chan struct{}, 1)
	if err := cache.Add(CallbackEntry{
		Inbox: "inbox.timeout",
		OnTimeout: func(context.Context) error {
			timedOut <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	select {
	case <-timedOut:
	case <-time.After(time.Second):
		t.Fatal("timeout callback was not called")
	}
}
