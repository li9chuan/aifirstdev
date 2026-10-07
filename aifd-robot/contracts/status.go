package contracts

// Status 是一次行为或 action tick 的结果。
type Status string

const (
	StatusSuccess Status = "success"
	StatusFailure Status = "failure"
	StatusRunning Status = "running"
)
