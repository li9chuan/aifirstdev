package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
)

func TestLoginCreatesProxySession(t *testing.T) {
	service := newTestService()
	result, err := service.Login(LoginRequest{Username: "demo", Password: "demo123"})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if !result.OK || result.SessionID == "" || !result.Session.Authenticated {
		t.Fatalf("Login result = %+v, want authenticated session", result)
	}
	workspace, err := service.Workspace(result.SessionID)
	if err != nil {
		t.Fatalf("Workspace error = %v", err)
	}
	if !workspace.Session.Authenticated || len(workspace.Tasks) == 0 {
		t.Fatalf("Workspace = %+v, want authenticated data", workspace)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	service := newTestService()
	result, err := service.Login(LoginRequest{Username: "demo", Password: "bad"})
	if err == nil || result.OK {
		t.Fatalf("Login result = %+v err=%v, want failure", result, err)
	}
	if result.Bootstrap.Message == "" {
		t.Fatal("Login failure returned empty bootstrap message")
	}
}

func TestWorkspaceRequiresSession(t *testing.T) {
	workspace, err := newTestService().Workspace("")
	if err == nil {
		t.Fatal("Workspace error = nil, want missing session")
	}
	if workspace.Session.Authenticated {
		t.Fatal("Workspace without session returned authenticated data")
	}
}

func TestLogoutClearsSession(t *testing.T) {
	service := newTestService()
	login, err := service.Login(LoginRequest{Username: "demo", Password: "demo123"})
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	bootstrap, err := service.Logout(login.SessionID)
	if err != nil {
		t.Fatalf("Logout error = %v", err)
	}
	if bootstrap.Session.Authenticated {
		t.Fatal("Logout returned authenticated bootstrap")
	}
	if _, err := service.Workspace(login.SessionID); err == nil {
		t.Fatal("Workspace after Logout error = nil, want missing session")
	}
}

func TestHTTPLoginWorkspaceLogout(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHTTPHandlers(mux, newTestService())
	server := httptest.NewServer(mux)
	defer server.Close()

	loginBody, _ := json.Marshal(LoginRequest{Username: "demo", Password: "demo123"})
	resp, err := http.Post(server.URL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("POST /api/login error = %v", err)
	}
	defer resp.Body.Close()
	var login LoginResult
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatalf("decode login error = %v", err)
	}
	if resp.StatusCode != http.StatusOK || login.SessionID == "" {
		t.Fatalf("login status=%d result=%+v, want session", resp.StatusCode, login)
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/workspace", bytes.NewReader([]byte("{}")))
	req.Header.Set(SessionHeader(), login.SessionID)
	workspaceResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/workspace error = %v", err)
	}
	defer workspaceResp.Body.Close()
	var workspace business.WorkspaceData
	if err := json.NewDecoder(workspaceResp.Body).Decode(&workspace); err != nil {
		t.Fatalf("decode workspace error = %v", err)
	}
	if workspaceResp.StatusCode != http.StatusOK || !workspace.Session.Authenticated {
		t.Fatalf("workspace status=%d result=%+v, want authenticated", workspaceResp.StatusCode, workspace)
	}

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/api/logout", bytes.NewReader([]byte("{}")))
	req.Header.Set(SessionHeader(), login.SessionID)
	logoutResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/logout error = %v", err)
	}
	_ = logoutResp.Body.Close()

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/api/workspace", bytes.NewReader([]byte("{}")))
	req.Header.Set(SessionHeader(), login.SessionID)
	afterLogoutResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/workspace after logout error = %v", err)
	}
	defer afterLogoutResp.Body.Close()
	if afterLogoutResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("workspace after logout status=%d, want 401", afterLogoutResp.StatusCode)
	}
}

func TestProxyUsesAccountAndBusinessHTTPClients(t *testing.T) {
	accountMux := http.NewServeMux()
	account.RegisterHTTPHandlers(accountMux, account.NewService())
	accountServer := httptest.NewServer(accountMux)
	defer accountServer.Close()

	businessMux := http.NewServeMux()
	business.RegisterHTTPHandlers(businessMux, business.NewService())
	businessServer := httptest.NewServer(businessMux)
	defer businessServer.Close()

	service := NewService(account.NewHTTPClient(accountServer.URL), business.NewHTTPClient(businessServer.URL))
	login, err := service.Login(LoginRequest{Username: "demo", Password: "demo123"})
	if err != nil {
		t.Fatalf("Login through HTTP clients error = %v", err)
	}
	if !login.OK || login.SessionID == "" || len(login.Workspace.Tasks) == 0 {
		t.Fatalf("Login through HTTP clients = %+v, want workspace and session", login)
	}
}

func newTestService() *Service {
	return NewService(account.NewService(), business.NewEmbeddedClient(business.NewService()))
}
