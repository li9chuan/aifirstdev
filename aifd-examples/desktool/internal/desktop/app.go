package desktop

import (
	"strings"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/updater"
)

// currentVersion is set by the Windows build entry through -ldflags.
var currentVersion = "0.1.0"

const updateManifestURL = ""

// LoginRequest 是前端提交给桌面绑定的登录请求。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult 是 Wails 登录绑定的返回值。
type LoginResult struct {
	OK        bool                   `json:"ok"`
	Message   string                 `json:"message"`
	Session   deskproxy.SessionState `json:"session"`
	Bootstrap business.BootstrapData `json:"bootstrap"`
	Workspace business.WorkspaceData `json:"workspace"`
}

// SessionState 表示当前桌面应用内存登录态。
type SessionState = deskproxy.SessionState

// BootstrapData 是桌面公开数据。
type BootstrapData = business.BootstrapData

// WorkspaceData 是登录后工作台数据。
type WorkspaceData = business.WorkspaceData

// UpdateInfo 是更新检查结果。
type UpdateInfo = updater.UpdateInfo

// DownloadResult 是更新包下载结果。
type DownloadResult = updater.DownloadResult

// UpdateResult 是安装更新结果。
type UpdateResult = updater.UpdateResult

// BackendClient 是桌面绑定访问后端的唯一门面。
type BackendClient interface {
	PublicHome() (BootstrapData, error)
	Login(LoginRequest) (LoginResult, error)
	Workspace() (WorkspaceData, error)
	Logout() (BootstrapData, error)
}

// Config 配置 DesktopApp 的后端模式和更新源。
type Config struct {
	Mode        string
	ProxyURL    string
	Backend     BackendClient
	Version     string
	ManifestURL string
}

// DesktopApp 是 Wails 暴露给前端的桌面门面。
type DesktopApp struct {
	session *sessionStore
	backend BackendClient

	updateMu       sync.Mutex
	lastUpdate     updater.UpdateInfo
	downloadedFile string

	currentVersion string
	manifestURL    string
}

// NewDesktopApp 创建桌面应用绑定。
func NewDesktopApp(configs ...Config) *DesktopApp {
	cfg := resolveConfig(configs...)
	return &DesktopApp{
		session:        &sessionStore{},
		backend:        cfg.Backend,
		currentVersion: cfg.Version,
		manifestURL:    cfg.ManifestURL,
	}
}

// Bootstrap 返回未登录可访问的公开桌面数据。
func (a *DesktopApp) Bootstrap() BootstrapData {
	if a == nil {
		return BootstrapData{Message: "应用未初始化"}
	}
	data, err := a.backend.PublicHome()
	if err != nil {
		return BootstrapData{Message: err.Error()}
	}
	a.session.Set(data.Session)
	return data
}

// CurrentSession 返回当前内存登录态。
func (a *DesktopApp) CurrentSession() SessionState {
	if a == nil {
		return SessionState{}
	}
	return a.session.Snapshot()
}

// Login 通过 proxy 后端门面建立登录态，并返回登录后的工作台数据。
func (a *DesktopApp) Login(req LoginRequest) LoginResult {
	if a == nil {
		return LoginResult{Message: "应用未初始化"}
	}
	result, err := a.backend.Login(req)
	if err != nil {
		current := a.CurrentSession()
		return LoginResult{
			Message:   err.Error(),
			Session:   current,
			Bootstrap: BootstrapData{Message: err.Error(), Session: current},
		}
	}
	if result.OK {
		a.session.Set(result.Session)
		return result
	}
	a.session.Clear()
	return result
}

// LoadWorkspace 返回当前登录用户的工作台数据。
func (a *DesktopApp) LoadWorkspace() WorkspaceData {
	if a == nil {
		return WorkspaceData{Message: "应用未初始化"}
	}
	data, err := a.backend.Workspace()
	if err != nil {
		return data
	}
	return data
}

// Logout 清空当前内存登录态，并返回公开桌面数据。
func (a *DesktopApp) Logout() BootstrapData {
	if a == nil {
		return BootstrapData{Message: "应用未初始化"}
	}
	data, err := a.backend.Logout()
	if err != nil {
		return BootstrapData{Message: err.Error(), Session: a.session.Clear()}
	}
	a.session.Clear()
	return data
}

// CheckUpdate 检查是否存在可安装的新版本。
func (a *DesktopApp) CheckUpdate() UpdateInfo {
	if a == nil {
		return UpdateInfo{CurrentVersion: currentVersion, Message: "应用未初始化"}
	}
	info := updater.Check(a.manifestURL, a.currentVersion)
	a.updateMu.Lock()
	a.lastUpdate = info
	if !info.Available {
		a.downloadedFile = ""
	}
	a.updateMu.Unlock()
	return info
}

// DownloadUpdate 下载最近一次检查到的更新包。
func (a *DesktopApp) DownloadUpdate() DownloadResult {
	if a == nil {
		return DownloadResult{Message: "应用未初始化"}
	}
	a.updateMu.Lock()
	info := a.lastUpdate
	a.updateMu.Unlock()
	if !info.Available {
		info = updater.Check(a.manifestURL, a.currentVersion)
	}
	result := updater.Download(info)
	a.updateMu.Lock()
	a.lastUpdate = result.Update
	if result.OK {
		a.downloadedFile = result.File
	}
	a.updateMu.Unlock()
	return result
}

// InstallUpdate 启动本地安装器。Windows 首版只支持 NSIS installer。
func (a *DesktopApp) InstallUpdate() UpdateResult {
	if a == nil {
		return UpdateResult{Message: "应用未初始化"}
	}
	a.updateMu.Lock()
	file := a.downloadedFile
	a.updateMu.Unlock()
	if file == "" {
		return UpdateResult{Message: "请先下载更新包"}
	}
	if err := updater.Install(file); err != nil {
		return UpdateResult{
			Unsupported: strings.HasPrefix(err.Error(), "unsupported platform"),
			Message:     err.Error(),
		}
	}
	return UpdateResult{OK: true, Message: "安装器已启动，请按提示完成安装"}
}
