package proto

import (
	"context"
	"fmt"
	"reflect"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	goproto "google.golang.org/protobuf/proto"
)

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	sessionType = reflect.TypeOf((*rpc.Session)(nil)).Elem()
	protoType   = reflect.TypeOf((*goproto.Message)(nil)).Elem()
	int64Type   = reflect.TypeOf(int64(0))
)

// Handler 将常见 protobuf handler 签名适配为 rpc.Handler。
func Handler(fn any) (rpc.Handler, error) {
	if fn == nil {
		return nil, fmt.Errorf("rpc proto handler is nil")
	}
	value := reflect.ValueOf(fn)
	fnType := value.Type()
	if fnType.Kind() != reflect.Func {
		return nil, fmt.Errorf("rpc proto handler must be func, got %s", fnType.Kind())
	}

	spec, err := inspect(fnType)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, session rpc.Session, msg rpc.Message) error {
		args := make([]reflect.Value, 0, fnType.NumIn())
		if spec.useContext {
			args = append(args, reflect.ValueOf(ctx))
		}
		if spec.useUID {
			args = append(args, reflect.ValueOf(session.UID()))
		}
		if spec.useSession {
			args = append(args, reflect.ValueOf(session))
		}
		if spec.messageType != nil {
			req := reflect.New(spec.messageType.Elem())
			if err := msg.DecodePB(req.Interface().(goproto.Message)); err != nil {
				return err
			}
			args = append(args, req)
		}

		var rets []reflect.Value
		var callErr error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					callErr = fmt.Errorf("rpc proto handler panic: %v", recovered)
				}
			}()
			rets = value.Call(args)
		}()
		if callErr != nil {
			return callErr
		}

		if len(rets) == 0 {
			return nil
		}
		if spec.errorOut >= 0 && !rets[spec.errorOut].IsNil() {
			return rets[spec.errorOut].Interface().(error)
		}
		if spec.responseOut >= 0 {
			ret := rets[spec.responseOut]
			if ret.Kind() == reflect.Interface || ret.Kind() == reflect.Ptr {
				if ret.IsNil() {
					return nil
				}
			}
			rsp, ok := ret.Interface().(goproto.Message)
			if !ok {
				return fmt.Errorf("rpc proto response must implement proto.Message")
			}
			return session.RespondPB(ctx, rsp)
		}
		return nil
	}, nil
}

type handlerSpec struct {
	useContext  bool
	useUID      bool
	useSession  bool
	messageType reflect.Type
	responseOut int
	errorOut    int
}

func inspect(fnType reflect.Type) (handlerSpec, error) {
	spec := handlerSpec{responseOut: -1, errorOut: -1}
	for i := 0; i < fnType.NumIn(); i++ {
		param := fnType.In(i)
		switch {
		case param == contextType:
			if spec.useContext {
				return spec, fmt.Errorf("rpc proto handler has duplicate context")
			}
			spec.useContext = true
		case param == int64Type:
			if spec.useUID || spec.useSession {
				return spec, fmt.Errorf("rpc proto handler has duplicate identity parameter")
			}
			spec.useUID = true
		case param.Implements(sessionType):
			if spec.useUID || spec.useSession {
				return spec, fmt.Errorf("rpc proto handler has duplicate identity parameter")
			}
			spec.useSession = true
		case param.Kind() == reflect.Ptr && param.Implements(protoType):
			if spec.messageType != nil {
				return spec, fmt.Errorf("rpc proto handler has duplicate proto request")
			}
			spec.messageType = param
		default:
			return spec, fmt.Errorf("rpc proto handler unsupported param[%d] %s", i, param)
		}
	}

	if fnType.NumOut() > 2 {
		return spec, fmt.Errorf("rpc proto handler has too many return values")
	}
	for i := 0; i < fnType.NumOut(); i++ {
		out := fnType.Out(i)
		switch {
		case out.Kind() == reflect.Ptr && out.Implements(protoType):
			if spec.responseOut >= 0 {
				return spec, fmt.Errorf("rpc proto handler has duplicate proto response")
			}
			spec.responseOut = i
		case out.Implements(reflect.TypeOf((*error)(nil)).Elem()):
			if spec.errorOut >= 0 {
				return spec, fmt.Errorf("rpc proto handler has duplicate error response")
			}
			spec.errorOut = i
		default:
			return spec, fmt.Errorf("rpc proto handler unsupported return[%d] %s", i, out)
		}
	}
	return spec, nil
}
