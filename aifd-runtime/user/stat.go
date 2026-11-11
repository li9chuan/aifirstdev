package user

import (
	"fmt"
	"reflect"

	"github.com/li9chuan/aifirstdev/aifd-log"
	"google.golang.org/protobuf/proto"
)

type statHeaderOption struct {
	userType reflect.Type
	build    func(any, logging.StatSchema) any
	validate func(...logging.StatSchema) error
}

// WithStatHeader registers an application-owned header builder, invoked for each
// User.Stat call before encoding and optional forwarding. It does not register
// header names or versions. The builder returns a concrete tagged struct and must
// read user data under the application's existing synchronization rules.
func WithStatHeader[T proto.Message, H any](build func(*User[T], logging.StatSchema) H) Option {
	return func(options *options) {
		options.statHeader = &statHeaderOption{
			userType: reflect.TypeFor[*User[T]](),
			build: func(owner any, event logging.StatSchema) any {
				return build(owner.(*User[T]), event)
			},
			validate: func(events ...logging.StatSchema) error {
				if build == nil {
					return fmt.Errorf("stat header builder is nil")
				}
				return logging.RegisterStatHeader[H](events...)
			},
		}
	}
}

// Stat records a typed payload using this user's configured application header.
// Header generation and local append finish in this call; forwarding receives
// only encoded text. Failures are counted/logged and never roll back business work.
// Call after RegisterRPC, with producer shutdown completed before logging closes.
func (o *User[T]) Stat(payload any) {
	var build func(logging.StatSchema) any
	if o != nil && o.statHeader != nil {
		build = func(event logging.StatSchema) any { return o.statHeader.build(o, event) }
	}
	_ = logging.RecordStat(payload, build)
}
