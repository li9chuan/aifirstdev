package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// HTTPClient 是内部服务调用账号中心 HTTP API 的客户端封装。
type HTTPClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPClient 创建账号中心 HTTP 客户端。
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Login 调用账号中心登录接口。
func (c *HTTPClient) Login(req LoginRequest) (LoginResult, error) {
	var result LoginResult
	if err := c.postJSON("/api/login", req, &result); err != nil {
		return result, err
	}
	if !result.OK {
		return result, ErrInvalidCredentials
	}
	return result, nil
}

// BindIdentity 调用账号中心身份绑定接口。
func (c *HTTPClient) BindIdentity(req BindIdentityRequest) (BindIdentityResult, error) {
	var result BindIdentityResult
	if err := c.postJSON("/api/bind-identity", req, &result); err != nil {
		return result, err
	}
	if !result.OK {
		return result, ErrIdentityConflict
	}
	return result, nil
}

func (c *HTTPClient) postJSON(path string, req interface{}, out interface{}) error {
	if c == nil || c.baseURL == "" {
		return fmt.Errorf("account http client is not configured")
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
		return fmt.Errorf("account http status %d", resp.StatusCode)
	}
	return nil
}

// RegisterHTTPHandlers 注册账号中心 HTTP API。
func RegisterHTTPHandlers(mux *http.ServeMux, service *Service) {
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.Login(req)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/api/bind-identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req BindIdentityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.BindIdentity(req)
		if err != nil {
			w.WriteHeader(http.StatusConflict)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}
