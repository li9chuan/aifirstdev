package user

import (
	"errors"
	"fmt"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// Client registers a user-aware client route. The runtime loads the trusted caller
// UID and injects the same cached user into every device's handler.
func (r *Runtime[T]) Client[Req proto.Message](topic string, handler func(*rpc.Session, *User[T], Req), options ...rpc.RouteOption) error {
	if err := r.check(); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("user client handler is nil")
	}
	return rpc.Client(r.reg, topic, func(s *rpc.Session, req Req) {
		if err := r.connections.ValidateCaller(s); err != nil {
			proxyruntime.RespondSessionError(s, err)
			return
		}
		owner, err := r.LoadUser(s.Context(), s.UID())
		if err != nil {
			r.respondLoadError(s, err)
			return
		}
		handler(s, owner, req)
	}, options...)
}

// Server registers a user-aware trusted service route. Loading offline users has
// no client connection or online side effects. Admin target UIDs are loaded explicitly.
func (r *Runtime[T]) Server[Req proto.Message](topic string, handler func(*rpc.Session, *User[T], Req), options ...rpc.RouteOption) error {
	if err := r.check(); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("user server handler is nil")
	}
	return rpc.Server(r.reg, topic, func(s *rpc.Session, req Req) {
		owner, err := r.LoadUser(s.Context(), s.UID())
		if err != nil {
			r.respondLoadError(s, err)
			return
		}
		handler(s, owner, req)
	}, options...)
}

func (r *Runtime[T]) respondLoadError(s *rpc.Session, err error) {
	code := rpc.CodeInternal
	if errors.Is(err, ErrNotFound) {
		code = "USER_NOT_FOUND"
	} else if errors.Is(err, ErrClosed) {
		code = "UNAVAILABLE"
	}
	s.Error(code, err.Error())
}
