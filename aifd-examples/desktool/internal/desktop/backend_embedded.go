package desktop

import (
	"errors"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
)

type embeddedBackendClient struct {
	mu        sync.Mutex
	proxy     *deskproxy.Service
	sessionID string
}

func newEmbeddedBackendClient() *embeddedBackendClient {
	accountService := account.NewService()
	businessService := business.NewService()
	return &embeddedBackendClient{
		proxy: deskproxy.NewService(accountService, business.NewEmbeddedClient(businessService)),
	}
}

func (c *embeddedBackendClient) PublicHome() (BootstrapData, error) {
	return c.proxy.Bootstrap()
}

func (c *embeddedBackendClient) Login(req LoginRequest) (LoginResult, error) {
	result, err := c.proxy.Login(deskproxy.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	c.mu.Lock()
	if result.OK {
		c.sessionID = result.SessionID
	} else {
		c.sessionID = ""
	}
	c.mu.Unlock()
	if errors.Is(err, deskproxy.ErrAuthFailed) {
		return desktopLoginResult(result), nil
	}
	return desktopLoginResult(result), err
}

func (c *embeddedBackendClient) Workspace() (WorkspaceData, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	data, err := c.proxy.Workspace(sessionID)
	if errors.Is(err, deskproxy.ErrSessionMissing) {
		return data, nil
	}
	return data, err
}

func (c *embeddedBackendClient) Logout() (BootstrapData, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.sessionID = ""
	c.mu.Unlock()
	return c.proxy.Logout(sessionID)
}

func desktopLoginResult(result deskproxy.LoginResult) LoginResult {
	return LoginResult{
		OK:        result.OK,
		Message:   result.Message,
		Session:   result.Session,
		Bootstrap: result.Bootstrap,
		Workspace: result.Workspace,
	}
}
