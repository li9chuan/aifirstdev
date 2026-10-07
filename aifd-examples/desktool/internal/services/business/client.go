package business

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/session"
)

// Client 是 proxy 调用业务数据服务的最小接口。
type Client interface {
	Bootstrap(session.State, string) (BootstrapData, error)
	Workspace(session.State, string) (WorkspaceData, error)
}

// EmbeddedClient 在本进程内直接调用业务服务。
type EmbeddedClient struct {
	service *Service
}

// NewEmbeddedClient 创建本进程业务数据客户端。
func NewEmbeddedClient(service *Service) *EmbeddedClient {
	if service == nil {
		service = NewService()
	}
	return &EmbeddedClient{service: service}
}

// Bootstrap 返回公开桌面数据。
func (c *EmbeddedClient) Bootstrap(state session.State, message string) (BootstrapData, error) {
	return c.service.Bootstrap(state, message), nil
}

// Workspace 返回登录后工作台数据。
func (c *EmbeddedClient) Workspace(state session.State, message string) (WorkspaceData, error) {
	return c.service.Workspace(state, message), nil
}

// HTTPClient 是 proxy 调用独立 business HTTP 服务的客户端。
type HTTPClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPClient 创建业务 HTTP 客户端。
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Bootstrap 调用业务公开数据接口。
func (c *HTTPClient) Bootstrap(state session.State, message string) (BootstrapData, error) {
	var result BootstrapData
	err := c.postJSON("/api/bootstrap", DataRequest{Session: state, Message: message}, &result)
	return result, err
}

// Workspace 调用业务工作台接口。
func (c *HTTPClient) Workspace(state session.State, message string) (WorkspaceData, error) {
	var result WorkspaceData
	err := c.postJSON("/api/workspace", DataRequest{Session: state, Message: message}, &result)
	return result, err
}

func (c *HTTPClient) postJSON(path string, req interface{}, out interface{}) error {
	if c == nil || c.baseURL == "" {
		return fmt.Errorf("business http client is not configured")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := c.client.Post(c.baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("business http status %d", resp.StatusCode)
	}
	return nil
}
