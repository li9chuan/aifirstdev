package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
)

func TestBootstrapStartsLoggedOutWithPublicData(t *testing.T) {
	app := NewDesktopApp()
	result := app.Bootstrap()
	if result.Session.Authenticated {
		t.Fatal("Bootstrap session starts authenticated")
	}
	if result.Title == "" || len(result.Nav) == 0 || len(result.Activity) == 0 {
		t.Fatalf("Bootstrap data incomplete: %+v", result)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	app := NewDesktopApp()
	result := app.Login(LoginRequest{Username: "demo", Password: "bad"})
	if result.OK {
		t.Fatal("Login OK = true, want false")
	}
	if result.Session.Authenticated || app.CurrentSession().Authenticated {
		t.Fatal("invalid login created session")
	}
	if result.Bootstrap.Message == "" {
		t.Fatal("invalid login returned empty bootstrap message")
	}
}

func TestLoginAcceptsDemoCredentials(t *testing.T) {
	app := NewDesktopApp()
	result := app.Login(LoginRequest{Username: " demo ", Password: " demo123 "})
	if !result.OK {
		t.Fatalf("Login OK = false, message=%q", result.Message)
	}
	if !result.Session.Authenticated || result.Session.Username != "demo" {
		t.Fatalf("session = %+v, want demo authenticated", result.Session)
	}
	if len(result.Workspace.Metrics) == 0 || len(result.Workspace.Tasks) == 0 {
		t.Fatalf("workspace = %+v, want sample data", result.Workspace)
	}
}

func TestLoadWorkspaceRequiresSession(t *testing.T) {
	app := NewDesktopApp()
	workspace := app.LoadWorkspace()
	if workspace.Session.Authenticated {
		t.Fatal("anonymous workspace returned authenticated session")
	}
	if workspace.Message == "" {
		t.Fatal("anonymous workspace message is empty")
	}
}

func TestLogoutClearsSessionAndReturnsBootstrap(t *testing.T) {
	app := NewDesktopApp()
	if result := app.Login(LoginRequest{Username: "demo", Password: "demo123"}); !result.OK {
		t.Fatalf("Login failed: %s", result.Message)
	}
	bootstrap := app.Logout()
	if bootstrap.Session.Authenticated {
		t.Fatal("Logout returned authenticated bootstrap")
	}
	if app.CurrentSession().Authenticated {
		t.Fatal("CurrentSession after Logout is authenticated")
	}
	if bootstrap.Message == "" {
		t.Fatal("Logout returned empty message")
	}
}

func TestCheckUpdateWithoutManifestIsDisplayable(t *testing.T) {
	app := NewDesktopApp()
	info := app.CheckUpdate()
	if info.Configured {
		t.Fatal("Configured = true, want false")
	}
	if info.CurrentVersion == "" || info.Message == "" {
		t.Fatalf("update info incomplete: %+v", info)
	}
}

func TestNewDesktopAppReadsUpdateEnv(t *testing.T) {
	t.Setenv("DESKTOOL_VERSION", "0.3.0")
	t.Setenv("DESKTOOL_UPDATE_MANIFEST_URL", "")
	app := NewDesktopApp()
	info := app.CheckUpdate()
	if info.CurrentVersion != "0.3.0" {
		t.Fatalf("CurrentVersion = %q, want env version", info.CurrentVersion)
	}
}

func TestNewDesktopAppUsesManifestEnv(t *testing.T) {
	sum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.2.0","url":"https://example.com/desktool.exe","sha256":"` + sum + `"}`))
	}))
	defer server.Close()
	t.Setenv("DESKTOOL_VERSION", "0.1.0")
	t.Setenv("DESKTOOL_UPDATE_MANIFEST_URL", server.URL)
	app := NewDesktopApp()
	info := app.CheckUpdate()
	if !info.Configured || !info.Available || info.Version != "0.2.0" {
		t.Fatalf("update info = %+v, want env manifest available", info)
	}
}

func TestInstallUpdateRequiresDownloadedFile(t *testing.T) {
	app := NewDesktopApp()
	result := app.InstallUpdate()
	if result.OK {
		t.Fatal("InstallUpdate OK = true, want false")
	}
	if result.Message == "" {
		t.Fatal("InstallUpdate message is empty")
	}
}

func TestRemoteBackendCallsProxyOnly(t *testing.T) {
	proxyService := deskproxy.NewService(
		account.NewService(),
		business.NewEmbeddedClient(business.NewService()),
	)
	mux := http.NewServeMux()
	deskproxy.RegisterHTTPHandlers(mux, proxyService)
	server := httptest.NewServer(mux)
	defer server.Close()

	app := NewDesktopApp(Config{Mode: modeRemote, ProxyURL: server.URL})
	bootstrap := app.Bootstrap()
	if bootstrap.Session.Authenticated {
		t.Fatal("remote Bootstrap returned authenticated session")
	}
	login := app.Login(LoginRequest{Username: "demo", Password: "demo123"})
	if !login.OK || !login.Session.Authenticated {
		t.Fatalf("remote Login = %+v, want authenticated", login)
	}
	workspace := app.LoadWorkspace()
	if !workspace.Session.Authenticated || len(workspace.Tasks) == 0 {
		t.Fatalf("remote Workspace = %+v, want authenticated data", workspace)
	}
	logout := app.Logout()
	if logout.Session.Authenticated || app.CurrentSession().Authenticated {
		t.Fatal("remote Logout left authenticated session")
	}
}
