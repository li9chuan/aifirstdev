package contracts

import "time"

// Kind 标识事件所属的 observability 流。
type Kind string

const (
	KindLog    Kind = "log"
	KindMetric Kind = "metric"
	KindTrace  Kind = "trace"
	KindAudit  Kind = "audit"
	KindStat   Kind = "stat"
	KindHealth Kind = "health"
)

// Level 主要用于日志事件。
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event 是日志、指标、trace、审计、统计和健康信号的共享 Envelope；它有意只携带框架级字段。
type Event struct {
	ID             string
	Kind           Kind
	Name           string
	Level          Level
	Message        string
	Time           time.Time
	SourceService  string
	SourceInstance string
	TraceID        string
	ShardKey       string
	Value          float64
	Dimensions     map[string]string
	Fields         map[string]any
}

// Clone 返回可安全放入异步队列和测试记录器的拷贝。
func (e Event) Clone() Event {
	out := e
	if !out.Time.IsZero() {
		out.Time = out.Time.UTC()
	}
	out.Dimensions = cloneStringMap(e.Dimensions)
	out.Fields = cloneAnyMap(e.Fields)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
