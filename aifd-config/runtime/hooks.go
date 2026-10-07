package runtime

import (
	"context"
	"fmt"
	"sync"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// ApplyFunc 发布 prepare 阶段构建出的模块私有视图或缓存。
type ApplyFunc func()

// PrepareFunc 在新 Bundle 提交前校验并构建可选 apply 函数。
//
// PrepareFunc 必须把 bundle 视为只读对象；它可以基于 bundle 构建模块私有视图或缓存，
// 但不能修改 Bundle.Data、Manifest 或依赖修改后的 bundle 被发布。
type PrepareFunc[T any] func(context.Context, *config.Bundle[T]) (ApplyFunc, error)

// Hook 命名一个参与配置 reload 的 prepare-then-commit 节点。
type Hook[T any] struct {
	Name    string
	Prepare PrepareFunc[T]
}

// HookManager 管理配置 reload hook；重复名称注册视为幂等。
type HookManager[T any] struct {
	mu    sync.RWMutex
	hooks []Hook[T]
}

type preparedHook struct {
	name  string
	apply ApplyFunc
}

// NewHookManager 创建空 hook 管理器。
func NewHookManager[T any]() *HookManager[T] {
	return &HookManager[T]{}
}

// Register 添加 reload hook，并将重复名称视为幂等。
func (m *HookManager[T]) Register(hook Hook[T]) error {
	if hook.Name == "" || hook.Prepare == nil {
		return fmt.Errorf("invalid config hook")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.hooks {
		if existing.Name == hook.Name {
			return nil
		}
	}
	m.hooks = append(m.hooks, hook)
	return nil
}

func (m *HookManager[T]) prepare(ctx context.Context, bundle *config.Bundle[T]) ([]preparedHook, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.RLock()
	hooks := append([]Hook[T](nil), m.hooks...)
	m.mu.RUnlock()

	prepared := make([]preparedHook, 0, len(hooks))
	for _, hook := range hooks {
		apply, err := hook.Prepare(ctx, bundle)
		if err != nil {
			return nil, fmt.Errorf("config hook %s prepare: %w", hook.Name, err)
		}
		prepared = append(prepared, preparedHook{name: hook.Name, apply: apply})
	}
	return prepared, nil
}

func applyPreparedHooks(prepared []preparedHook) {
	for _, hook := range prepared {
		if hook.apply != nil {
			hook.apply()
		}
	}
}
