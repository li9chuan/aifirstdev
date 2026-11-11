package user

import (
	"fmt"
	"reflect"

	"github.com/li9chuan/aifirstdev/aifd-log"
	"google.golang.org/protobuf/proto"
)

type statHeaderOption struct {
	userType reflect.Type
	build    func(any, string) any
	validate func() error
}

// WithStatHeader configures an application-owned header builder, invoked for each
// User.Stat call before encoding and optional forwarding. It does not register
// header names or versions. eventName is the payload's struct type name. The
// builder returns a concrete tagged struct and must read user data under the
// application's existing synchronization rules.
func WithStatHeader[T proto.Message, H any](build func(owner *User[T], eventName string) H) Option {
	return func(options *options) {
		options.statHeader = &statHeaderOption{
			userType: reflect.TypeFor[*User[T]](),
			build: func(owner any, eventName string) any {
				return build(owner.(*User[T]), eventName)
			},
			validate: func() error {
				if build == nil {
					return fmt.Errorf("stat header builder is nil")
				}
				return logging.ValidateStatHeader[H]()
			},
		}
	}
}

// Stat records a named struct or non-nil struct pointer using this user's header.
// Header generation and local append finish in this call; forwarding receives
// only encoded text. Failures are counted/logged and never roll back business work.
// The struct type name identifies the event, with no module registration. Stop
// producers before logging closes; header reads follow application locking rules.
func (o *User[T]) Stat(payload any) {
	var build func(string) any
	if o != nil && o.statHeader != nil {
		build = func(eventName string) any { return o.statHeader.build(o, eventName) }
	}
	_ = logging.RecordStat(payload, build)
}
