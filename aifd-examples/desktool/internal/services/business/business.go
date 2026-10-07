package business

import "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/session"

// NavItem 是桌面壳侧边导航项。
type NavItem struct {
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

// ActivityItem 是工作台活动日志项。
type ActivityItem struct {
	Text string `json:"text"`
}

// Metric 是登录后工作台指标。
type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Task 是登录后工作台任务行。
type Task struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Time   string `json:"time"`
}

// BootstrapData 是未登录也可读取的公开桌面数据。
type BootstrapData struct {
	AppTitle string         `json:"appTitle"`
	Mode     string         `json:"mode"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Message  string         `json:"message"`
	Session  session.State  `json:"session"`
	Nav      []NavItem      `json:"nav"`
	Activity []ActivityItem `json:"activity"`
}

// WorkspaceData 是登录后工作台数据。
type WorkspaceData struct {
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Message  string         `json:"message"`
	Session  session.State  `json:"session"`
	Metrics  []Metric       `json:"metrics"`
	Tasks    []Task         `json:"tasks"`
	Activity []ActivityItem `json:"activity"`
}

// Service 是 desktool 示例业务服务。
type Service struct{}

// NewService 创建业务服务。
func NewService() *Service {
	return &Service{}
}

// Bootstrap 返回未登录也可查看的公开工作台数据。
func (s *Service) Bootstrap(state session.State, message string) BootstrapData {
	if message == "" {
		message = "公开基础数据已加载，可选择是否登录"
	}
	return BootstrapData{
		AppTitle: "DeskTool",
		Mode:     "AI-first Desktop",
		Title:    "桌面工作台",
		Summary:  "这是普通 Wails + React + Ant Design 渲染的公开主界面。",
		Message:  message,
		Session:  state,
		Nav:      defaultNav(),
		Activity: []ActivityItem{
			{Text: "Wails desktop shell initialized"},
			{Text: "Public bootstrap data loaded"},
			{Text: "Waiting for optional login"},
		},
	}
}

// Workspace 返回登录后的示例工作台数据。
func (s *Service) Workspace(state session.State, message string) WorkspaceData {
	if message == "" {
		message = "工作台已刷新"
	}
	summary := state.DisplayName + " 的示例工作台。"
	if !state.Authenticated {
		message = "请先登录后查看工作台数据"
		summary = "登录后可查看个人工作台数据。"
	}
	return WorkspaceData{
		Title:   "桌面工作台",
		Summary: summary,
		Message: message,
		Session: state,
		Metrics: []Metric{
			{Label: "Tasks", Value: "12"},
			{Label: "Reports", Value: "4"},
			{Label: "Alerts", Value: "0"},
		},
		Tasks: []Task{
			{Name: "示例任务 A", Status: "Ready", Time: "09:30"},
			{Name: "示例报告 B", Status: "Draft", Time: "10:15"},
			{Name: "示例同步 C", Status: "Done", Time: "11:00"},
		},
		Activity: []ActivityItem{
			{Text: "Account identity verified by proxy service"},
			{Text: "Workspace data loaded by business service"},
			{Text: "User action channel is ready"},
		},
	}
}

func defaultNav() []NavItem {
	return []NavItem{
		{Label: "概览", Active: true},
		{Label: "工作区"},
		{Label: "报告"},
		{Label: "设置"},
	}
}
