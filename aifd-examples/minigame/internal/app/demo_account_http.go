package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	"google.golang.org/protobuf/proto"
)

// serveDemoAuth is deliberately insecure local demonstration provisioning.
// It issues from the proxy's authority; clients no longer invent local sessions.
func (a *AccountApp) serveDemoAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Subject int64 `json:"subject"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF || input.Subject <= 0 {
		http.Error(w, "invalid demo subject", http.StatusBadRequest)
		return
	}
	auth, err := a.DemoAuth(r.Context(), input.Subject)
	if err != nil {
		http.Error(w, "demo account unavailable", http.StatusServiceUnavailable)
		return
	}
	raw, err := proto.Marshal(auth)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// RequestDemoAuth is only for this example's development client and robot.
func RequestDemoAuth(ctx context.Context, websocketURL string, subject int64) (*pbmsg.MceAuth, error) {
	u, err := url.Parse(websocketURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return nil, fmt.Errorf("invalid proxy URL")
	}
	u.Path = "/demo/auth"
	u.RawQuery = ""
	u.Fragment = ""
	raw, _ := json.Marshal(struct {
		Subject int64 `json:"subject"`
	}{subject})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rsp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("demo account returned HTTP %d", rsp.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(rsp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, fmt.Errorf("invalid demo account response")
	}
	var auth pbmsg.MceAuth
	if err = proto.Unmarshal(raw, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}
