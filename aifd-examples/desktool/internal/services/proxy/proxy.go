package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/session"
)

var (
	ErrAuthFailed     = errors.New("auth failed")
	ErrSessionMissing = errors.New("proxy session missing")
)

// SessionState 是 proxy 认证后绑定到连接或 HTTP 会话上的身份状态。
type SessionState = session.State

// AccountClient 是 proxy 调用账号中心的最小接口。
type AccountClient interface {
	Login(account.LoginRequest) (account.LoginResult, error)
}

// DataClient 是 proxy 调用业务数据服务的最小接口。
type DataClient interface {
	Bootstrap(session.State, string) (business.BootstrapData, error)
	Workspace(session.State, string) (business.WorkspaceData, error)
}

// LoginRequest 是客户端发给 proxy 的登录请求。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult 是 proxy 登录入口返回给客户端的结果。
type LoginResult struct {
	OK        bool                   `json:"ok"`
	Message   string                 `json:"message"`
	SessionID string                 `json:"sessionID,omitempty"`
	Session   session.State          `json:"session"`
	Bootstrap business.BootstrapData `json:"bootstrap"`
	Workspace business.WorkspaceData `json:"workspace"`
}

// Service 是 desktool 示例 proxy 后端入口。
type Service struct {
	account AccountClient
	data    DataClient

	mu       sync.RWMutex
	sessions map[string]session.State
}

// NewService 创建 proxy 服务。
func NewService(accountClient AccountClient, dataClient DataClient) *Service {
	return &Service{
		account:  accountClient,
		data:     dataClient,
		sessions: make(map[string]session.State),
	}
}

// Bootstrap 返回未登录也可访问的公开桌面数据。
func (s *Service) Bootstrap() (business.BootstrapData, error) {
	if s == nil || s.data == nil {
		return business.BootstrapData{Message: "proxy 未初始化"}, ErrSessionMissing
	}
	return s.data.Bootstrap(session.State{}, "")
}

// Login 调用账号中心并在 proxy 内部建立服务端 session。
func (s *Service) Login(req LoginRequest) (LoginResult, error) {
	if s == nil || s.account == nil || s.data == nil {
		return LoginResult{Message: "proxy 未初始化"}, ErrAuthFailed
	}
	accountResult, err := s.account.Login(account.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil || !accountResult.OK {
		message := accountResult.Message
		if message == "" {
			message = "账号或密码不正确"
		}
		bootstrap, _ := s.data.Bootstrap(session.State{}, message)
		return LoginResult{
			Message:   message,
			Bootstrap: bootstrap,
		}, ErrAuthFailed
	}
	state := session.State{
		Authenticated: true,
		AccountID:     accountResult.AccountID,
		UID:           accountResult.UID,
		Username:      accountResult.Username,
		DisplayName:   accountResult.DisplayName,
		Role:          "Workspace Admin",
	}
	sessionID := newSessionID()
	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()
	workspace, err := s.data.Workspace(state, accountResult.Message)
	if err != nil {
		return LoginResult{Message: err.Error(), Session: state}, err
	}
	return LoginResult{
		OK:        true,
		Message:   accountResult.Message,
		SessionID: sessionID,
		Session:   state,
		Workspace: workspace,
	}, nil
}

// Workspace 根据 proxy 内部 session 返回工作台数据。
func (s *Service) Workspace(sessionID string) (business.WorkspaceData, error) {
	if s == nil || s.data == nil {
		return business.WorkspaceData{Message: "proxy 未初始化"}, ErrSessionMissing
	}
	state, ok := s.lookup(sessionID)
	if !ok {
		data, _ := s.data.Workspace(session.State{}, "请先登录后查看工作台数据")
		return data, ErrSessionMissing
	}
	return s.data.Workspace(state, "")
}

// Logout 清理 proxy 内部 session 并返回公开桌面数据。
func (s *Service) Logout(sessionID string) (business.BootstrapData, error) {
	if s == nil || s.data == nil {
		return business.BootstrapData{Message: "proxy 未初始化"}, ErrSessionMissing
	}
	if sessionID != "" {
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
	}
	return s.data.Bootstrap(session.State{}, "已退出登录")
}

func (s *Service) lookup(sessionID string) (session.State, bool) {
	if sessionID == "" {
		return session.State{}, false
	}
	s.mu.RLock()
	state, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	return state, ok
}

func newSessionID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "sess-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("sess-%d", time.Now().UnixNano())
}
