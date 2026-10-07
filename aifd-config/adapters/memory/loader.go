package memory

import (
	"context"
	"fmt"
	"sync"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// Loader 在内存中保存一个可替换的强类型配置包，用于单测和示例。
type Loader[T any] struct {
	mu     sync.RWMutex
	bundle *config.Bundle[T]
}

// NewLoader 创建内存配置 Loader。
func NewLoader[T any](bundle *config.Bundle[T]) *Loader[T] {
	return &Loader[T]{bundle: bundle}
}

// SetBundle 替换下一次 Load 返回的配置包。
func (l *Loader[T]) SetBundle(bundle *config.Bundle[T]) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bundle = bundle
}

// Load 返回当前内存配置包。
func (l *Loader[T]) Load(context.Context) (*config.Bundle[T], error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.bundle == nil {
		return nil, fmt.Errorf("config bundle is nil")
	}
	return l.bundle, nil
}
