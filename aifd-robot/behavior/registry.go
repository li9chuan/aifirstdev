package behavior

import (
	"fmt"
	"sort"
	"sync"

	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

// Registry 存储行为 action factory。
type Registry struct {
	mu        sync.RWMutex
	factories map[string]robot.ActionFactory
}

// NewRegistry 创建空行为 action registry。
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]robot.ActionFactory)}
}

// Register 按名称存储 action factory，并拒绝重复注册。
func (r *Registry) Register(reg robot.ActionRegistration) error {
	if reg.Name == "" || reg.Factory == nil {
		return fmt.Errorf("invalid behavior action registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[reg.Name]; exists {
		return fmt.Errorf("behavior action %s already registered", reg.Name)
	}
	r.factories[reg.Name] = reg.Factory
	return nil
}

// Build 通过已注册的 factory 创建新的 action 实例。
func (r *Registry) Build(name string, settings robot.Settings) (robot.Action, error) {
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("behavior action %s not registered", name)
	}
	return factory(settings), nil
}

// Names 返回稳定排序后的已注册 action 名称。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
