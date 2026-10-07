package console

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// Sink 将 JSON line 写入 io.Writer。
type Sink struct {
	mu sync.Mutex
	w  io.Writer
}

// New 创建 console Sink；writer 为空时默认写 stdout。
func New(w io.Writer) *Sink {
	if w == nil {
		w = os.Stdout
	}
	return &Sink{w: w}
}

// Write 在 Sink 锁下将每个事件序列化为一行 JSON。
func (s *Sink) Write(_ context.Context, events []contracts.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoder := json.NewEncoder(s.w)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("console observe encode: %w", err)
		}
	}
	return nil
}
