package contracts

// Logger 有意保持极小，使基础设施模块不必依赖具体日志框架。
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
