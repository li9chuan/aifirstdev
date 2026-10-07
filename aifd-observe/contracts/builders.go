package contracts

import "time"

// Log 创建日志事件。
func Log(level Level, name, message string) Event {
	return Event{Kind: KindLog, Level: level, Name: name, Message: message, Time: time.Now().UTC()}
}

// Metric 创建指标增量或 gauge 事件。
func Metric(name string, value float64, dimensions map[string]string) Event {
	return Event{Kind: KindMetric, Name: name, Value: value, Dimensions: cloneStringMap(dimensions), Time: time.Now().UTC()}
}

// Stat 创建业务统计事件；统计事件应保留原始事件身份，以便下游聚合去重和回放。
func Stat(id, name, shardKey string, value float64, dimensions map[string]string) Event {
	return Event{ID: id, Kind: KindStat, Name: name, ShardKey: shardKey, Value: value, Dimensions: cloneStringMap(dimensions), Time: time.Now().UTC()}
}

// Audit 为重要操作创建审计事件。
func Audit(id, name, message string, fields map[string]any) Event {
	return Event{ID: id, Kind: KindAudit, Name: name, Message: message, Fields: cloneAnyMap(fields), Time: time.Now().UTC()}
}
