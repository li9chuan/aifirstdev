package wechat

import (
	"context"
	"fmt"
	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestCodeExchangeDoesNotReplayLostReply(t *testing.T) {
	var attempts atomic.Int32
	client := testHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") == "warm" {
			fmt.Fprint(w, `{"openid":"open","access_token":"token"}`)
			return
		}
		if attempts.Add(1) == 1 {
			c, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			c.Close()
			return
		}
		fmt.Fprint(w, `{"errcode":40163}`)
	})
	if _, err := client.ExchangeWebsiteCode(context.Background(), "app", "secret", "warm"); err != nil {
		t.Fatal(err)
	}
	_, err := client.ExchangeWebsiteCode(context.Background(), "app", "secret", "one-time-code")
	t.Logf("one ExchangeWebsiteCode call: upstream attempts=%d returned=%v", attempts.Load(), err)
	if attempts.Load() != 1 || err != credential.ErrUnavailable {
		t.Fatalf("one-time code replay or wrong error: %d %v", attempts.Load(), err)
	}
}
