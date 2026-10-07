package nats

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNewBusIntegrationSkipWithoutEnv(t *testing.T) {
	url := os.Getenv("AIFD_NATS_URL")
	if url == "" {
		t.Skip("set AIFD_NATS_URL to run NATS integration tests")
	}
	bus, err := NewBus(Config{
		Options: natsgo.Options{
			Servers: []string{url},
			Name:    "aifd-rpc-test",
			Timeout: time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewBus error = %v", err)
	}
	defer bus.Close(context.Background())
}

func TestRequestPBReturnsStatusErrorIntegration(t *testing.T) {
	url := os.Getenv("AIFD_NATS_URL")
	if url == "" {
		t.Skip("set AIFD_NATS_URL to run NATS integration tests")
	}
	bus, err := NewBus(Config{
		Options: natsgo.Options{
			Servers: []string{url},
			Name:    "aifd-rpc-test-status",
			Timeout: time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewBus error = %v", err)
	}
	defer bus.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rpcruntime.Run(ctx, bus.Deliveries(), rpcruntime.RunnerOptions{Workers: 1, QueueSize: 8})

	if err := bus.Register(rpc.Route{
		Name: "PBError",
		Handler: func(ctx context.Context, session rpc.Session, _ rpc.Message) error {
			return session.Respond(ctx, rpc.Message{
				Name:    "RPCError",
				Payload: []byte("not protobuf"),
				Header: map[string]string{
					rpc.HeaderStatus:       "INVALID",
					rpc.HeaderErrorMessage: "bad request",
				},
			})
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if err := bus.nc.FlushTimeout(time.Second); err != nil {
		t.Fatalf("NATS flush error = %v", err)
	}

	var rsp wrapperspb.StringValue
	err = bus.RequestPB(ctx, 1, "PBError", wrapperspb.String("hello"), &rsp, rpc.RequestOptions{Timeout: time.Second})
	var statusErr *rpc.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("RequestPB error = %v, want StatusError", err)
	}
	if statusErr.Code != "INVALID" || statusErr.Message != "bad request" {
		t.Fatalf("status error = %+v, want INVALID bad request", statusErr)
	}
}
