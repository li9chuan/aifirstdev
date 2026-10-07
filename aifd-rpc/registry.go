package rpc

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

var (
	sessionType = reflect.TypeOf((*Session)(nil))
	protoType   = reflect.TypeOf((*proto.Message)(nil)).Elem()
)

type routeSpec struct {
	name    string
	kind    contracts.RouteKind
	reqType reflect.Type
	handler reflect.Value
	options routeOptions
}

// Registry 保存业务 PB route 注册。
type Registry struct {
	mu     sync.RWMutex
	routes map[string]routeSpec
	order  []string
}

// NewRegistry 创建空业务 route 注册表。
func NewRegistry() *Registry {
	return &Registry{routes: make(map[string]routeSpec)}
}

// Client 注册客户端入口 route。
func Client(reg *Registry, name string, handler any, options ...RouteOption) error {
	return register(reg, name, contracts.RouteKindClient, handler, options...)
}

// Server 注册服务间入口 route。
func Server(reg *Registry, name string, handler any, options ...RouteOption) error {
	return register(reg, name, contracts.RouteKindServer, handler, options...)
}

// Admin 注册管理入口 route。
func Admin(reg *Registry, name string, handler any, options ...RouteOption) error {
	return register(reg, name, contracts.RouteKindAdmin, handler, options...)
}

// Stream 注册可靠事件 route。
func Stream(reg *Registry, name string, handler any, options ...RouteOption) error {
	return register(reg, name, contracts.RouteKindStream, handler, options...)
}

func register(reg *Registry, name string, kind contracts.RouteKind, handler any, options ...RouteOption) error {
	if reg == nil {
		return fmt.Errorf("rpc registry is nil")
	}
	if name == "" {
		return fmt.Errorf("rpc route name is empty")
	}
	spec, err := inspectHandler(name, kind, handler)
	if err != nil {
		return err
	}
	spec.options = applyRouteOptions(options)

	reg.mu.Lock()
	defer reg.mu.Unlock()
	if _, exists := reg.routes[name]; exists {
		return fmt.Errorf("rpc route %s already registered", name)
	}
	reg.routes[name] = spec
	reg.order = append(reg.order, name)
	return nil
}

func inspectHandler(name string, kind contracts.RouteKind, handler any) (routeSpec, error) {
	if handler == nil {
		return routeSpec{}, fmt.Errorf("rpc route %s handler is nil", name)
	}
	value := reflect.ValueOf(handler)
	fnType := value.Type()
	if fnType.Kind() != reflect.Func {
		return routeSpec{}, fmt.Errorf("rpc route %s handler must be func", name)
	}
	if fnType.NumOut() != 0 {
		return routeSpec{}, fmt.Errorf("rpc route %s handler must not return values", name)
	}
	if fnType.NumIn() != 1 && fnType.NumIn() != 2 {
		return routeSpec{}, fmt.Errorf("rpc route %s handler must be func(*Session) or func(*Session, *Req)", name)
	}
	if fnType.In(0) != sessionType {
		return routeSpec{}, fmt.Errorf("rpc route %s first parameter must be *rpc.Session", name)
	}
	var reqType reflect.Type
	if fnType.NumIn() == 2 {
		reqType = fnType.In(1)
		if reqType.Kind() != reflect.Ptr || !reqType.Implements(protoType) {
			return routeSpec{}, fmt.Errorf("rpc route %s request parameter must implement proto.Message", name)
		}
	}
	return routeSpec{name: name, kind: kind, reqType: reqType, handler: value}, nil
}

func (r *Registry) specs() []routeSpec {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]routeSpec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.routes[name])
	}
	return out
}

// Install 将业务注册表安装到真实 Bus。
func Install(bus contracts.Bus, reg *Registry) error {
	return InstallContext(context.Background(), bus, reg)
}

// InstallContext 将业务注册表安装到真实 Bus，并为 stream 注册提供 context。
func InstallContext(ctx context.Context, bus contracts.Bus, reg *Registry) error {
	if bus == nil {
		return fmt.Errorf("rpc install bus is nil")
	}
	for _, spec := range reg.specs() {
		route := contracts.Route{
			Name:    spec.name,
			Kind:    spec.kind,
			Service: spec.options.service,
			Handler: spec.wrap(bus),
			Queue:   spec.options.queue,
			Timeout: spec.options.timeout,
		}
		if spec.kind == contracts.RouteKindStream {
			streamBus, ok := bus.(contracts.StreamBus)
			if !ok {
				return fmt.Errorf("rpc route %s requires stream bus", spec.name)
			}
			if err := streamBus.RegisterStream(ctx, route); err != nil {
				return err
			}
			continue
		}
		if spec.options.queue != "" {
			if err := bus.RegisterQueue(route, spec.options.queue); err != nil {
				return err
			}
			continue
		}
		if err := bus.Register(route); err != nil {
			return err
		}
	}
	return nil
}

func (s routeSpec) wrap(bus contracts.Bus) contracts.Handler {
	return func(ctx context.Context, baseSession contracts.Session, msg contracts.Message) (err error) {
		session := newSession(ctx, bus, baseSession, msg)
		defer func() {
			if recovered := recover(); recovered != nil {
				session.Error(CodeInternal, "rpc handler panic")
				err = fmt.Errorf("rpc route %s panic: %v", s.name, recovered)
			}
		}()

		args := []reflect.Value{reflect.ValueOf(session)}
		if s.reqType != nil {
			req := reflect.New(s.reqType.Elem())
			if decodeErr := msg.DecodePB(req.Interface().(proto.Message)); decodeErr != nil {
				session.Error(CodeDecode, decodeErr.Error())
				return decodeErr
			}
			args = append(args, req)
		}
		s.handler.Call(args)
		return nil
	}
}
