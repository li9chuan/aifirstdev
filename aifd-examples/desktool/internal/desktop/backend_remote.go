package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
)

type remoteBackendClient struct {
	baseURL string
	client  *http.Client

	mu        sync.Mutex
	sessionID string
}

func newRemoteBackendClient(baseURL string) *remoteBackendClient {
	return &remoteBackendClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *remoteBackendClient) PublicHome() (BootstrapData, error) {
	var result BootstrapData
	err := c.doJSON(http.MethodGet, "/api/bootstrap", "", nil, &result)
	return result, err
}

func (c *remoteBackendClient) Login(req LoginRequest) (LoginResult, error) {
	var result deskproxy.LoginResult
	err := c.doJSON(http.MethodPost, "/api/login", "", req, &result)
	c.mu.Lock()
	if result.OK {
		c.sessionID = result.SessionID
	} else {
		c.sessionID = ""
	}
	c.mu.Unlock()
	if err != nil && result.Message == "" {
		return LoginResult{}, err
	}
	return desktopLoginResult(result), nil
}

func (c *remoteBackendClient) Workspace() (WorkspaceData, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	var result WorkspaceData
	err := c.doJSON(http.MethodPost, "/api/workspace", sessionID, struct{}{}, &result)
	if result.Message != "" {
		return result, nil
	}
	return result, err
}

func (c *remoteBackendClient) Logout() (BootstrapData, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.sessionID = ""
	c.mu.Unlock()
	var result BootstrapData
	err := c.doJSON(http.MethodPost, "/api/logout", sessionID, struct{}{}, &result)
	return result, err
}

func (c *remoteBackendClient) doJSON(method string, path string, sessionID string, req interface{}, out interface{}) error {
	if c == nil || c.baseURL == "" {
		return fmt.Errorf("proxy http client is not configured")
	}
	var body *bytes.Reader
	if req == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	httpReq, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if req != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if sessionID != "" {
		httpReq.Header.Set(deskproxy.SessionHeader(), sessionID)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("proxy http status %d", resp.StatusCode)
	}
	return nil
}
