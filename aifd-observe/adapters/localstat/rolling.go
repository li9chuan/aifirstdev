package localstat

import (
	"sync"
	"time"
)

type rollingBucket struct {
	value float64
	count int64
}

type rollingWindow struct {
	mu          sync.Mutex
	buckets     []rollingBucket
	bucketWidth time.Duration
	current     int
	last        time.Time
	now         func() time.Time
}

// RollingCounter 是固定时间窗口内的计数器。
type RollingCounter struct {
	window *rollingWindow
}

// RollingSummary 是固定时间窗口内的 sum/count 聚合器。
type RollingSummary struct {
	window *rollingWindow
}

// NewRollingCounter 创建滚动窗口计数器。
func NewRollingCounter(window time.Duration, bucketCount int) *RollingCounter {
	return &RollingCounter{window: newRollingWindow(window, bucketCount, time.Now)}
}

// NewRollingSummary 创建滚动窗口 summary。
func NewRollingSummary(window time.Duration, bucketCount int) *RollingSummary {
	return &RollingSummary{window: newRollingWindow(window, bucketCount, time.Now)}
}

func newRollingWindow(window time.Duration, bucketCount int, now func() time.Time) *rollingWindow {
	if bucketCount <= 0 {
		bucketCount = 1
	}
	if window <= 0 {
		window = time.Second
	}
	if now == nil {
		now = time.Now
	}
	width := window / time.Duration(bucketCount)
	if width <= 0 {
		width = window
	}
	return &rollingWindow{
		buckets:     make([]rollingBucket, bucketCount),
		bucketWidth: width,
		now:         now,
	}
}

// Add 增加计数值。
func (c *RollingCounter) Add(value float64) {
	c.window.add(value)
}

// Value 返回当前窗口内的计数总和。
func (c *RollingCounter) Value() float64 {
	value, _ := c.window.value()
	return value
}

// Reset 清空当前窗口。
func (c *RollingCounter) Reset() {
	c.window.reset()
}

// Add 增加一次 summary 样本。
func (s *RollingSummary) Add(value float64) {
	s.window.add(value)
}

// Value 返回当前窗口内的总和与样本数。
func (s *RollingSummary) Value() (float64, int64) {
	return s.window.value()
}

// Reset 清空当前窗口。
func (s *RollingSummary) Reset() {
	s.window.reset()
}

func (w *rollingWindow) add(value float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance(w.now())
	w.buckets[w.current].value += value
	w.buckets[w.current].count++
}

func (w *rollingWindow) value() (float64, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance(w.now())
	var sum float64
	var count int64
	for _, bucket := range w.buckets {
		sum += bucket.value
		count += bucket.count
	}
	return sum, count
}

func (w *rollingWindow) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.buckets {
		w.buckets[i] = rollingBucket{}
	}
	w.last = w.now()
	w.current = 0
}

func (w *rollingWindow) advance(now time.Time) {
	if w.last.IsZero() {
		w.last = now
		return
	}
	steps := int(now.Sub(w.last) / w.bucketWidth)
	if steps <= 0 {
		return
	}
	if steps >= len(w.buckets) {
		for i := range w.buckets {
			w.buckets[i] = rollingBucket{}
		}
		w.current = 0
		w.last = now
		return
	}
	for i := 0; i < steps; i++ {
		w.current = (w.current + 1) % len(w.buckets)
		w.buckets[w.current] = rollingBucket{}
	}
	w.last = w.last.Add(time.Duration(steps) * w.bucketWidth)
}
