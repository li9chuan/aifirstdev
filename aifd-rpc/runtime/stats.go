package runtime

import (
	"sort"
	"strings"
	"sync"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

const defaultStatsWindowSec int64 = 60

type sizeStatKind int

const (
	sizeReqOut sizeStatKind = iota + 1
	sizeReqIn
	sizeRspOut
	sizeRspIn
)

type sizeAgg struct {
	count int64
	total int64
	min   int64
	max   int64
}

func (a *sizeAgg) add(size int64) {
	if a.count == 0 || size < a.min {
		a.min = size
	}
	if a.count == 0 || size > a.max {
		a.max = size
	}
	a.count++
	a.total += size
}

func (a *sizeAgg) merge(other sizeAgg) {
	if other.count == 0 {
		return
	}
	if a.count == 0 || other.min < a.min {
		a.min = other.min
	}
	if a.count == 0 || other.max > a.max {
		a.max = other.max
	}
	a.count += other.count
	a.total += other.total
}

func (a sizeAgg) snapshot() rpc.SizeAgg {
	out := rpc.SizeAgg{
		Count:      a.count,
		TotalBytes: a.total,
		MinBytes:   a.min,
		MaxBytes:   a.max,
	}
	if a.count > 0 {
		out.AvgBytes = float64(a.total) / float64(a.count)
	}
	return out
}

type latencyAgg struct {
	count int64
	total int64
	min   int64
	max   int64
}

func (a *latencyAgg) add(durationUs int64) {
	if durationUs < 0 {
		durationUs = 0
	}
	if a.count == 0 || durationUs < a.min {
		a.min = durationUs
	}
	if a.count == 0 || durationUs > a.max {
		a.max = durationUs
	}
	a.count++
	a.total += durationUs
}

func (a *latencyAgg) merge(other latencyAgg) {
	if other.count == 0 {
		return
	}
	if a.count == 0 || other.min < a.min {
		a.min = other.min
	}
	if a.count == 0 || other.max > a.max {
		a.max = other.max
	}
	a.count += other.count
	a.total += other.total
}

func (a latencyAgg) snapshot() rpc.LatencyAgg {
	out := rpc.LatencyAgg{
		Count:   a.count,
		TotalUs: a.total,
		MinUs:   a.min,
		MaxUs:   a.max,
	}
	if a.count > 0 {
		out.AvgUs = float64(a.total) / float64(a.count)
	}
	return out
}

type sizeBucket struct {
	sec int64
	agg sizeAgg
}

type latencyBucket struct {
	sec int64
	agg latencyAgg
}

type sizeMetric struct {
	total   sizeAgg
	buckets []sizeBucket
}

func newSizeMetric(window int64) sizeMetric {
	return sizeMetric{buckets: make([]sizeBucket, window)}
}

func (m *sizeMetric) add(nowSec, size int64) {
	m.total.add(size)
	idx := nowSec % int64(len(m.buckets))
	bucket := &m.buckets[idx]
	if bucket.sec != nowSec {
		bucket.sec = nowSec
		bucket.agg = sizeAgg{}
	}
	bucket.agg.add(size)
}

func (m *sizeMetric) recent(nowSec, window int64) sizeAgg {
	var agg sizeAgg
	for _, bucket := range m.buckets {
		if bucket.agg.count == 0 {
			continue
		}
		if nowSec-bucket.sec >= window || bucket.sec > nowSec {
			continue
		}
		agg.merge(bucket.agg)
	}
	return agg
}

type latencyMetric struct {
	total   latencyAgg
	buckets []latencyBucket
}

func newLatencyMetric(window int64) latencyMetric {
	return latencyMetric{buckets: make([]latencyBucket, window)}
}

func (m *latencyMetric) add(nowSec, durationUs int64) {
	m.total.add(durationUs)
	idx := nowSec % int64(len(m.buckets))
	bucket := &m.buckets[idx]
	if bucket.sec != nowSec {
		bucket.sec = nowSec
		bucket.agg = latencyAgg{}
	}
	bucket.agg.add(durationUs)
}

func (m *latencyMetric) recent(nowSec, window int64) latencyAgg {
	var agg latencyAgg
	for _, bucket := range m.buckets {
		if bucket.agg.count == 0 {
			continue
		}
		if nowSec-bucket.sec >= window || bucket.sec > nowSec {
			continue
		}
		agg.merge(bucket.agg)
	}
	return agg
}

type statsEntry struct {
	mu     sync.Mutex
	reqOut sizeMetric
	reqIn  sizeMetric
	rspOut sizeMetric
	rspIn  sizeMetric
	handle latencyMetric
}

func newStatsEntry(window int64) *statsEntry {
	return &statsEntry{
		reqOut: newSizeMetric(window),
		reqIn:  newSizeMetric(window),
		rspOut: newSizeMetric(window),
		rspIn:  newSizeMetric(window),
		handle: newLatencyMetric(window),
	}
}

// StatsCollector 记录每个 route 的大小和 handler 延迟指标。
type StatsCollector struct {
	serviceName string
	windowSec   int64
	now         func() time.Time
	entries     sync.Map
}

// NewStatsCollector 创建 collector；非正数窗口使用 60 秒。
func NewStatsCollector(serviceName string, window time.Duration) *StatsCollector {
	windowSec := int64(window / time.Second)
	if windowSec <= 0 {
		windowSec = defaultStatsWindowSec
	}
	return &StatsCollector{
		serviceName: serviceName,
		windowSec:   windowSec,
		now:         time.Now,
	}
}

func normalizeRouteName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimPrefix(name, "RPC.")
}

func (c *StatsCollector) getEntry(name string) *statsEntry {
	if c == nil {
		return nil
	}
	method := normalizeRouteName(name)
	if method == "" {
		return nil
	}
	if entry, ok := c.entries.Load(method); ok {
		return entry.(*statsEntry)
	}
	entry := newStatsEntry(c.windowSec)
	actual, _ := c.entries.LoadOrStore(method, entry)
	return actual.(*statsEntry)
}

func (c *StatsCollector) recordSize(name string, size int, kind sizeStatKind) {
	entry := c.getEntry(name)
	if entry == nil {
		return
	}
	nowSec := c.now().Unix()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	switch kind {
	case sizeReqOut:
		entry.reqOut.add(nowSec, int64(size))
	case sizeReqIn:
		entry.reqIn.add(nowSec, int64(size))
	case sizeRspOut:
		entry.rspOut.add(nowSec, int64(size))
	case sizeRspIn:
		entry.rspIn.add(nowSec, int64(size))
	}
}

// RecordReqOut 记录 route 的出站请求 payload 大小。
func (c *StatsCollector) RecordReqOut(name string, size int) {
	c.recordSize(name, size, sizeReqOut)
}

// RecordReqIn 记录 route 的入站请求 payload 大小。
func (c *StatsCollector) RecordReqIn(name string, size int) {
	c.recordSize(name, size, sizeReqIn)
}

// RecordRspOut 记录 route 的出站响应 payload 大小。
func (c *StatsCollector) RecordRspOut(name string, size int) {
	c.recordSize(name, size, sizeRspOut)
}

// RecordRspIn 记录 route 的入站响应 payload 大小。
func (c *StatsCollector) RecordRspIn(name string, size int) {
	c.recordSize(name, size, sizeRspIn)
}

// RecordHandle 记录 route 的 handler 延迟。
func (c *StatsCollector) RecordHandle(name string, duration time.Duration) {
	entry := c.getEntry(name)
	if entry == nil {
		return
	}
	nowSec := c.now().Unix()
	entry.mu.Lock()
	entry.handle.add(nowSec, duration.Microseconds())
	entry.mu.Unlock()
}

// Snapshot 按稳定 route 顺序返回总量和近期指标。
func (c *StatsCollector) Snapshot() rpc.StatsSnapshot {
	if c == nil {
		return rpc.StatsSnapshot{WindowSec: defaultStatsWindowSec}
	}
	now := c.now()
	nowSec := now.Unix()
	stats := make([]rpc.RouteStats, 0)
	c.entries.Range(func(key, value any) bool {
		method := key.(string)
		entry := value.(*statsEntry)
		entry.mu.Lock()
		total := rpc.StatView{
			ReqOut: entry.reqOut.total.snapshot(),
			ReqIn:  entry.reqIn.total.snapshot(),
			RspOut: entry.rspOut.total.snapshot(),
			RspIn:  entry.rspIn.total.snapshot(),
			Handle: entry.handle.total.snapshot(),
		}
		recent := rpc.StatView{
			ReqOut: entry.reqOut.recent(nowSec, c.windowSec).snapshot(),
			ReqIn:  entry.reqIn.recent(nowSec, c.windowSec).snapshot(),
			RspOut: entry.rspOut.recent(nowSec, c.windowSec).snapshot(),
			RspIn:  entry.rspIn.recent(nowSec, c.windowSec).snapshot(),
			Handle: entry.handle.recent(nowSec, c.windowSec).snapshot(),
		}
		entry.mu.Unlock()
		stats = append(stats, rpc.RouteStats{Name: method, Total: total, Recent: recent})
		return true
	})
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Name < stats[j].Name
	})
	return rpc.StatsSnapshot{
		Service:     c.serviceName,
		GeneratedAt: now.Unix(),
		WindowSec:   c.windowSec,
		Stats:       stats,
	}
}
