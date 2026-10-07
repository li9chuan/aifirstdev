package account

import (
	"errors"
	"strings"
	"sync"
)

const (
	DemoUsername  = "demo"
	DemoPassword  = "demo123"
	DemoUID       = int64(1001)
	DemoAccountID = "acct-demo"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrIdentityConflict   = errors.New("account identity conflict")
)

// Account 是账号中心的账号真相源。
type Account struct {
	AccountID   string `json:"accountID"`
	UID         int64  `json:"uid"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

// AccountIdentity 表示账号绑定的一种登录身份。
type AccountIdentity struct {
	AccountID  string `json:"accountID"`
	Provider   string `json:"provider"`
	ExternalID string `json:"externalID"`
	Verified   bool   `json:"verified"`
}

// LoginRequest 是账号中心收到的登录请求。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult 是账号中心签发给桌面端的登录结果。
type LoginResult struct {
	OK          bool   `json:"ok"`
	AccountID   string `json:"accountID"`
	UID         int64  `json:"uid"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Message     string `json:"message"`
}

// BindIdentityRequest 是绑定第三方或手机号身份的请求。
type BindIdentityRequest struct {
	AccountID  string `json:"accountID"`
	Provider   string `json:"provider"`
	ExternalID string `json:"externalID"`
}

// BindIdentityResult 是身份绑定结果。
type BindIdentityResult struct {
	OK       bool            `json:"ok"`
	Identity AccountIdentity `json:"identity"`
	Message  string          `json:"message"`
}

// Service 是 desktool 示例账号中心。
type Service struct {
	mu         sync.Mutex
	accounts   map[string]Account
	identities map[string]AccountIdentity
}

// NewService 创建账号中心服务。
func NewService() *Service {
	account := Account{
		AccountID:   DemoAccountID,
		UID:         DemoUID,
		Username:    DemoUsername,
		DisplayName: "Demo User",
	}
	return &Service{
		accounts: map[string]Account{
			account.AccountID: account,
		},
		identities: map[string]AccountIdentity{
			identityKey("password", DemoUsername): {
				AccountID:  account.AccountID,
				Provider:   "password",
				ExternalID: DemoUsername,
				Verified:   true,
			},
		},
	}
}

// Login 校验 demo 账号并返回账号身份，不向客户端签发 JWT。
func (s *Service) Login(req LoginRequest) (LoginResult, error) {
	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username != DemoUsername || password != DemoPassword {
		return LoginResult{Message: "账号或密码不正确"}, ErrInvalidCredentials
	}
	account := s.accounts[DemoAccountID]
	return LoginResult{
		OK:          true,
		AccountID:   account.AccountID,
		UID:         account.UID,
		Username:    account.Username,
		DisplayName: account.DisplayName,
		Message:     "登录成功",
	}, nil
}

// BindIdentity 把一种登录身份绑定到已有账号。
func (s *Service) BindIdentity(req BindIdentityRequest) (BindIdentityResult, error) {
	accountID := strings.TrimSpace(req.AccountID)
	provider := strings.TrimSpace(req.Provider)
	externalID := strings.TrimSpace(req.ExternalID)
	if accountID == "" || provider == "" || externalID == "" {
		return BindIdentityResult{Message: "绑定参数不完整"}, ErrInvalidCredentials
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return BindIdentityResult{Message: "账号不存在"}, ErrInvalidCredentials
	}
	key := identityKey(provider, externalID)
	if existing, ok := s.identities[key]; ok {
		if existing.AccountID == accountID {
			return BindIdentityResult{OK: true, Identity: existing, Message: "身份已绑定"}, nil
		}
		return BindIdentityResult{Message: "身份已绑定到其他账号"}, ErrIdentityConflict
	}
	identity := AccountIdentity{
		AccountID:  accountID,
		Provider:   provider,
		ExternalID: externalID,
		Verified:   true,
	}
	s.identities[key] = identity
	return BindIdentityResult{OK: true, Identity: identity, Message: "绑定成功"}, nil
}

func identityKey(provider string, externalID string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" + strings.TrimSpace(externalID)
}
