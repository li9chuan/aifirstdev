package memory

import (
	"context"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// Recorder 在内存中保存事件，用于测试。
type Recorder struct {
	mu     sync.Mutex
	events []contracts.Event
}

// NewRecorder 创建空的内存事件记录器。
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Write 追加克隆后的事件，使调用方可安全复用输入。
func (r *Recorder) Write(_ context.Context, events []contracts.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range events {
		r.events = append(r.events, event.Clone())
	}
	return nil
}

// Events 返回已记录事件的切片快照。
func (r *Recorder) Events() []contracts.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]contracts.Event, len(r.events))
	copy(out, r.events)
	return out
}

// Reset 清空所有已记录事件。
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}
