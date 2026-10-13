package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestHeartbeatResetsMissesAndNotifiesOnce(t *testing.T) {
	client := newTestClient(t, newTestTransport(), Options{})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var calls atomic.Int32
	lost := make(chan error, 2)
	probeErr := errors.New("business heartbeat rejected")
	if err := client.StartHeartbeat(HeartbeatOptions{Interval: time.Millisecond, Timeout: time.Second, MaxMisses: 2,
		Probe: func(context.Context) error {
			if calls.Add(1) == 2 {
				return nil
			}
			return probeErr
		},
		OnLost: func(err error) { lost <- err; _ = client.Close() },
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lost:
		if !errors.Is(err, probeErr) || calls.Load() != 4 {
			t.Fatalf("lost=%v calls=%d", err, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after consecutive misses")
	}
	select {
	case err := <-lost:
		t.Fatalf("duplicate notification: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestStopHeartbeatCancelsPendingTypedRequest(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	finished := make(chan error, 1)
	lost := make(chan error, 1)
	opts := HeartbeatOptions{Interval: time.Millisecond, Timeout: time.Second, MaxMisses: 1,
		Probe: func(ctx context.Context) error {
			err := client.Request(ctx, wrapperspb.String("heartbeat"), &wrapperspb.StringValue{})
			finished <- err
			return err
		},
		OnLost: func(err error) { lost <- err },
	}
	if err := client.StartHeartbeat(opts); err != nil {
		t.Fatal(err)
	}
	if err := client.StartHeartbeat(opts); !errors.Is(err, ErrHeartbeatRunning) {
		t.Fatalf("duplicate start: %v", err)
	}
	_ = waitForSent(t, transport, 1)
	client.StopHeartbeat()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("probe cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe retained pending request")
	}
	select {
	case err := <-lost:
		t.Fatalf("intentional stop reported loss: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHeartbeatDeadlineCountsAsFailure(t *testing.T) {
	client := newTestClient(t, newTestTransport(), Options{})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	lost := make(chan error, 1)
	if err := client.StartHeartbeat(HeartbeatOptions{Interval: time.Millisecond, Timeout: 5 * time.Millisecond, MaxMisses: 1,
		Probe: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, OnLost: func(err error) { lost <- err },
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lost:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe timeout was ignored")
	}
}
