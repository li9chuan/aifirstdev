package entrypoint

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServerUsesBoundedConnectionDefaults(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	server := NewHTTPServer(handler)
	if server.Handler == nil {
		t.Fatal("Handler is nil")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 15*time.Second {
		t.Fatalf("ReadTimeout = %v, want 15s", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Fatalf("WriteTimeout = %v, want 30s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %v, want 60s", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want 1 MiB", server.MaxHeaderBytes)
	}
}

func TestCloseRunsComponentsInReverseOrder(t *testing.T) {
	var order []string
	err := Close(context.Background(),
		Component{Name: "first", Close: func(context.Context) error {
			order = append(order, "first")
			return nil
		}},
		Component{Name: "second", Close: func(context.Context) error {
			order = append(order, "second")
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if got, want := order, []string{"second", "first"}; !sameStrings(got, want) {
		t.Fatalf("close order = %v, want %v", got, want)
	}
}

func TestCloseWrapsComponentName(t *testing.T) {
	errBroken := errors.New("broken")
	err := Close(context.Background(), Component{Name: "bus", Close: func(context.Context) error {
		return errBroken
	}})
	if !errors.Is(err, errBroken) {
		t.Fatalf("Close error = %v, want %v", err, errBroken)
	}
	if err == nil || err.Error() != "bus close: broken" {
		t.Fatalf("Close error text = %v, want component name", err)
	}
}

func TestTriggerPanicCrashCallsHandlerOnce(t *testing.T) {
	crashing.Store(false)
	defer SetCrashHandler(nil)
	called := 0
	SetCrashHandler(func(info CrashInfo) {
		called++
		if info.Source != "test" || info.PanicValue != "boom" {
			t.Fatalf("crash info = %+v, want source test panic boom", info)
		}
	})
	if !TriggerPanicCrash("test", "boom") {
		t.Fatal("first TriggerPanicCrash = false, want true")
	}
	if TriggerPanicCrash("test2", "boom2") {
		t.Fatal("second TriggerPanicCrash = true, want false")
	}
	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
