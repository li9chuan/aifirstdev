package session

// State 是 proxy 认证后绑定到连接或后端会话上的身份状态。
type State struct {
	Authenticated bool   `json:"authenticated"`
	AccountID     string `json:"accountID"`
	UID           int64  `json:"uid"`
	Username      string `json:"username"`
	DisplayName   string `json:"displayName"`
	Role          string `json:"role"`
}
