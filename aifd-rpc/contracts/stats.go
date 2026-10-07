package contracts

// SizeAgg 聚合消息 payload 大小。
type SizeAgg struct {
	Count      int64
	TotalBytes int64
	MinBytes   int64
	MaxBytes   int64
	AvgBytes   float64
}

// LatencyAgg 以微秒聚合 handler 延迟。
type LatencyAgg struct {
	Count   int64
	TotalUs int64
	MinUs   int64
	MaxUs   int64
	AvgUs   float64
}

// StatView 汇总单个时间范围内的大小和延迟指标。
type StatView struct {
	ReqOut SizeAgg
	ReqIn  SizeAgg
	RspOut SizeAgg
	RspIn  SizeAgg
	Handle LatencyAgg
}

// RouteStats 包含单个 route 的总量和近期指标。
type RouteStats struct {
	Name   string
	Total  StatView
	Recent StatView
}

// StatsSnapshot 是框架级 RPC 统计视图。
type StatsSnapshot struct {
	Service     string
	GeneratedAt int64
	WindowSec   int64
	Stats       []RouteStats
}

// Stats 暴露统计快照，不绑定具体 collector 实现。
type Stats interface {
	Snapshot() StatsSnapshot
}
