package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

func TestACMERetryAfterPersistsAndGatesRequests(t *testing.T) {
	for _, status := range []int{429, 503} {
		for _, dateFormat := range []bool{false, true} {
			name := http.StatusText(status)
			if dateFormat {
				name += "/date"
			} else {
				name += "/seconds"
			}
			t.Run(name, func(t *testing.T) {
				now := time.Now().UTC().Truncate(time.Second)
				var requests atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						header := "120"
						if dateFormat {
							header = now.Add(2 * time.Minute).Format(http.TimeFormat)
						}
						w.Header().Set("Retry-After", header)
						w.WriteHeader(status)
						return
					}
					w.WriteHeader(200)
				}))
				defer server.Close()
				disk := &certmagic.FileStorage{Path: t.TempDir()}
				retry, err := newACMERetry(context.Background(), disk)
				if err != nil {
					t.Fatal(err)
				}
				retry.now = func() time.Time { return now }
				transport := &acmeRetryTransport{base: server.Client().Transport, retry: retry}
				client := &http.Client{Transport: transport}
				response, err := client.Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				key := "ca/" + server.URL
				if got := retry.entries[key].NotBefore; !got.Equal(now.Add(2 * time.Minute)) {
					t.Fatalf("deadline = %v", got)
				}
				assertRequestBlocked(t, client, server.URL)
				if requests.Load() != 1 {
					t.Fatal("request escaped cooldown")
				}
				// Construct fresh state and transport, as after a process restart.
				restarted, err := newACMERetry(context.Background(), disk)
				if err != nil {
					t.Fatal(err)
				}
				restarted.now = func() time.Time { return now }
				fresh := &http.Client{Transport: &acmeRetryTransport{base: server.Client().Transport, retry: restarted}}
				assertRequestBlocked(t, fresh, server.URL)
				if requests.Load() != 1 {
					t.Fatal("restart reset cooldown")
				}
				restarted.now = func() time.Time { return now.Add(2*time.Minute + time.Second) }
				response, err = fresh.Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if requests.Load() != 2 || len(restarted.entries) != 0 {
					t.Fatal("expired cooldown did not recover and reset")
				}
			})
		}
	}
}

func assertRequestBlocked(t *testing.T, client *http.Client, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation while cooling down, got %v", err)
	}
}

func TestACMERetryFallbackNeverShortensDeadline(t *testing.T) {
	retry, err := newACMERetry(context.Background(), &certmagic.FileStorage{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	retry.now = func() time.Time { return now }
	if err := retry.failed("ca/test", "3600"); err != nil {
		t.Fatal(err)
	}
	if err := retry.failed("ca/test", "invalid"); err != nil {
		t.Fatal(err)
	}
	if got := retry.entries["ca/test"].NotBefore; !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("later error shortened deadline: %v", got)
	}
	if err := retry.succeeded("ca/test"); err != nil {
		t.Fatal(err)
	}
	if len(retry.entries) != 1 {
		t.Fatal("in-flight success cleared an unexpired cooldown")
	}
	if err := retry.failed("certificate/one.test", ""); err != nil {
		t.Fatal(err)
	}
	if retry.entries["certificate/one.test"].NotBefore.Before(now.Add(30 * time.Second)) {
		t.Fatal("missing header did not use fallback backoff")
	}
	if err := retry.wait(context.Background(), "certificate/another.test"); err != nil {
		t.Fatal("unrelated certificate was blocked")
	}
}

func TestACMERetryRejectsCorruptStateAndStopsAfterWriteFailure(t *testing.T) {
	ctx := context.Background()
	disk := &certmagic.FileStorage{Path: t.TempDir()}
	for _, data := range []string{"{", `{"version":2,"entries":{}}`, `{"version":1,"entries":{"x":{"failures":0}}}`} {
		if err := disk.Store(ctx, retryStateKey, []byte(data)); err != nil {
			t.Fatal(err)
		}
		if _, err := newACMERetry(ctx, disk); err == nil {
			t.Fatalf("accepted invalid cooldown: %s", data)
		}
	}
	if err := disk.Delete(ctx, retryStateKey); err != nil {
		t.Fatal(err)
	}
	fault := &retryFailStorage{Storage: disk}
	retry, err := newACMERetry(ctx, fault)
	if err != nil {
		t.Fatal(err)
	}
	if err := retry.failed("ca/test", "60"); err == nil {
		t.Fatal("ignored cooldown persistence failure")
	}
	if err := retry.wait(ctx, "another-key"); err == nil {
		t.Fatal("requests remained enabled after storage failure")
	}
}

type retryFailStorage struct{ certmagic.Storage }

func (s *retryFailStorage) Store(context.Context, string, []byte) error {
	return errors.New("injected cooldown storage failure")
}

func TestACMERetryCancellationDoesNotSendOrRecordFailure(t *testing.T) {
	retry, err := newACMERetry(context.Background(), &certmagic.FileStorage{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	transport := &acmeRetryTransport{retry: retry, base: retryRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ca.example.test/dir", nil)
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(retry.entries) != 0 {
		t.Fatal("cancelled request sent traffic or changed failure state")
	}
}

type retryRoundTripper func(*http.Request) (*http.Response, error)

func (f retryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestACMERetryKeepsResponseDeadlineAfterCancellation(t *testing.T) {
	disk := &certmagic.FileStorage{Path: t.TempDir()}
	retry, err := newACMERetry(context.Background(), disk)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &acmeRetryTransport{retry: retry, base: retryRoundTripper(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"600"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://ca.example.test/order", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	restarted, err := newACMERetry(context.Background(), disk)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.entries["ca/https://ca.example.test"].NotBefore; got.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("cancellation lost the received cooldown: %v", got)
	}
}
