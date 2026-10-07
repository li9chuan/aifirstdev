package runtime

import (
	"context"
	"errors"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// KindRouter routes events to sinks by Event.Kind.
type KindRouter struct {
	routes   map[contracts.Kind]contracts.Sink
	fallback contracts.Sink
}

// NewKindRouter creates a router with an optional fallback sink.
func NewKindRouter(fallback contracts.Sink) *KindRouter {
	return &KindRouter{
		routes:   make(map[contracts.Kind]contracts.Sink),
		fallback: fallback,
	}
}

// Set installs or replaces the sink for kind. A nil sink drops that kind.
func (r *KindRouter) Set(kind contracts.Kind, sink contracts.Sink) {
	if r == nil {
		return
	}
	if r.routes == nil {
		r.routes = make(map[contracts.Kind]contracts.Sink)
	}
	r.routes[kind] = sink
}

// Write routes each event to the sink configured for its kind.
func (r *KindRouter) Write(ctx context.Context, events []contracts.Event) error {
	if r == nil {
		return nil
	}
	var joined error
	for _, event := range events {
		sink, ok := r.routes[event.Kind]
		if !ok {
			sink = r.fallback
		}
		if sink == nil {
			continue
		}
		if err := sink.Write(ctx, []contracts.Event{event}); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}
