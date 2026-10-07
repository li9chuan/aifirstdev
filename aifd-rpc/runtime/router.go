package runtime

import (
	"fmt"
	"sort"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Router 是进程内 route registry 和 dispatcher。
type Router struct {
	mu     sync.RWMutex
	routes map[string]rpc.Route
}

// NewRouter 创建空 router。
func NewRouter() *Router {
	return &Router{routes: make(map[string]rpc.Route)}
}

// Register 添加 route；重复名称会被拒绝，确保生成 registry 和 AI review 中的 route 归属保持显式。
func (r *Router) Register(route rpc.Route) error {
	if route.Name == "" {
		return fmt.Errorf("rpc route name is empty")
	}
	if route.Handler == nil {
		return fmt.Errorf("rpc route %s handler is nil", route.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.routes[route.Name]; exists {
		return fmt.Errorf("rpc route %s already registered", route.Name)
	}
	r.routes[route.Name] = route
	return nil
}

// RegisterQueue 注册带 queue 投递元数据的 route。
func (r *Router) RegisterQueue(route rpc.Route, queue string) error {
	route.Queue = queue
	return r.Register(route)
}

// RegisterStream 注册可靠 stream route。
func (r *Router) RegisterStream(route rpc.Route) error {
	route.Kind = rpc.RouteKindStream
	return r.Register(route)
}

// Get 按消息名返回 route。
func (r *Router) Get(name string) (rpc.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, ok := r.routes[name]
	return route, ok
}

// Routes 返回已注册 route 的稳定快照。
func (r *Router) Routes() []rpc.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.routes))
	for name := range r.routes {
		names = append(names, name)
	}
	sort.Strings(names)

	routes := make([]rpc.Route, 0, len(names))
	for _, name := range names {
		routes = append(routes, r.routes[name])
	}
	return routes
}
