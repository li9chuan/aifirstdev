package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// Manager 拥有已提交配置包，并按 prepare-then-commit 语义执行 reload。
type Manager[T any] struct {
	mu         sync.RWMutex
	loader     config.Loader[T]
	validators []config.Validator[T]
	hooks      HookManager[T]
	current    *config.Bundle[T]
	loaded     bool
}

// NewManager 创建配置管理器；BasicValidator 始终先于项目校验器运行。
func NewManager[T any](loader config.Loader[T], validators ...config.Validator[T]) (*Manager[T], error) {
	if loader == nil {
		return nil, fmt.Errorf("config loader is nil")
	}
	allValidators := make([]config.Validator[T], 0, len(validators)+1)
	allValidators = append(allValidators, BasicValidator[T]{})
	allValidators = append(allValidators, validators...)
	return &Manager[T]{loader: loader, validators: allValidators}, nil
}

// RegisterHook 注册模块级配置热更 hook。
func (m *Manager[T]) RegisterHook(hook Hook[T]) error {
	return m.hooks.Register(hook)
}

// Current 返回当前已提交的强类型配置包。
//
// 返回的 Bundle 由 Manager 按指针发布；调用方必须将 Bundle.Data 视为不可变对象。
func (m *Manager[T]) Current() (*config.Bundle[T], bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.loaded {
		return nil, false
	}
	return m.current, true
}

// Reload 加载并校验新配置包；error 级校验发现会阻止提交，并保留旧配置包。
func (m *Manager[T]) Reload(ctx context.Context) (config.ReloadResult, error) {
	next, err := m.loader.Load(ctx)
	if err != nil {
		return config.ReloadResult{}, err
	}
	if next == nil {
		return config.ReloadResult{}, fmt.Errorf("config bundle is nil")
	}
	if next.LoadedAt.IsZero() {
		next.LoadedAt = time.Now().UTC()
	}

	m.mu.RLock()
	previous := m.current
	loaded := m.loaded
	m.mu.RUnlock()

	var issues []config.Issue
	for _, validator := range m.validators {
		issues = append(issues, validator.Validate(ctx, next)...)
	}
	if !loaded {
		previous = nil
	}
	result := config.ReloadResult{
		ChangeSet:  Diff(previous, next),
		NewVersion: next.Version,
		Issues:     issues,
	}
	if previous != nil {
		result.OldVersion = previous.Version
	}
	if hasErrorIssue(issues) {
		return result, nil
	}
	preparedHooks, err := m.hooks.prepare(ctx, next)
	if err != nil {
		return result, err
	}

	m.mu.Lock()
	m.current = next
	m.loaded = true
	m.mu.Unlock()
	applyPreparedHooks(preparedHooks)
	result.Applied = true
	return result, nil
}
