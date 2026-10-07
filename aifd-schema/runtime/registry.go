package runtime

import (
	"fmt"
	"sort"
	"sync"

	schema "github.com/li9chuan/aifirstdev/aifd-schema/contracts"
)

// Registry 是供测试、工具和生成注册胶水使用的内存 schema registry。
type Registry struct {
	mu     sync.RWMutex
	byName map[string]schema.MessageDescriptor
	byID   map[schema.MessageID]string
}

// NewRegistry 创建空 schema registry。
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]schema.MessageDescriptor),
		byID:   make(map[schema.MessageID]string),
	}
}

// Register 添加一个描述，并拒绝重复名称或重复的非零 ID。
func (r *Registry) Register(desc schema.MessageDescriptor) error {
	if desc.Name == "" {
		return fmt.Errorf("schema message name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[desc.Name]; exists {
		return fmt.Errorf("schema message %s already registered", desc.Name)
	}
	if desc.ID != 0 {
		if owner, exists := r.byID[desc.ID]; exists {
			return fmt.Errorf("schema message id %d already used by %s", desc.ID, owner)
		}
		r.byID[desc.ID] = desc.Name
	}
	r.byName[desc.Name] = desc.Clone()
	return nil
}

// GetByName 按消息名返回描述。
func (r *Registry) GetByName(name string) (schema.MessageDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.byName[name]
	return desc.Clone(), ok
}

// GetByID 按非零消息 ID 返回描述。
func (r *Registry) GetByID(id schema.MessageID) (schema.MessageDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.byID[id]
	if !ok {
		return schema.MessageDescriptor{}, false
	}
	return r.byName[name].Clone(), true
}

// Messages 返回按消息名稳定排序的快照。
func (r *Registry) Messages() []schema.MessageDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]schema.MessageDescriptor, 0, len(names))
	for _, name := range names {
		out = append(out, r.byName[name].Clone())
	}
	return out
}
