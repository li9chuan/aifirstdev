package localstat

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// Store 是面向开发环境和小服务的单进程统计聚合器；它保留原始统计事件用于回放式检查，并按名称、时间桶和维度聚合。
type Store struct {
	mu    sync.Mutex
	raw   map[string]contracts.Event
	aggs  map[AggregateKey]float64
	seen  map[string]struct{}
	width time.Duration
}

// AggregateKey 标识一个聚合桶。
type AggregateKey struct {
	Name       string
	BucketUnix int64
	Dimensions string
}

// NewStore 创建本地统计存储；未设置宽度时使用分钟桶。
func NewStore(bucketWidth time.Duration) *Store {
	if bucketWidth <= 0 {
		bucketWidth = time.Minute
	}
	return &Store{
		raw:   make(map[string]contracts.Event),
		aggs:  make(map[AggregateKey]float64),
		seen:  make(map[string]struct{}),
		width: bucketWidth,
	}
}

// Write 按事件 ID 仅记录一次统计事件，并按桶聚合。
func (s *Store) Write(_ context.Context, events []contracts.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		if event.Kind != contracts.KindStat {
			continue
		}
		if event.ID != "" {
			if _, exists := s.seen[event.ID]; exists {
				continue
			}
			s.seen[event.ID] = struct{}{}
			s.raw[event.ID] = event.Clone()
		}
		key := AggregateKey{
			Name:       event.Name,
			BucketUnix: event.Time.Truncate(s.width).Unix(),
			Dimensions: encodeDimensions(event.Dimensions),
		}
		s.aggs[key] += event.Value
	}
	return nil
}

// RawEvents 返回按事件 ID 排序的原始统计事件拷贝。
func (s *Store) RawEvents() []contracts.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]contracts.Event, 0, len(s.raw))
	for _, event := range s.raw {
		events = append(events, event.Clone())
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].ID < events[j].ID
	})
	return events
}

// Aggregates 返回当前聚合桶总量的拷贝。
func (s *Store) Aggregates() map[AggregateKey]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[AggregateKey]float64, len(s.aggs))
	for key, value := range s.aggs {
		out[key] = value
	}
	return out
}

func encodeDimensions(dimensions map[string]string) string {
	if len(dimensions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+dimensions[key])
	}
	return strings.Join(parts, ",")
}
