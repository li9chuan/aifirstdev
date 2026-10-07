package rdb

import (
	"reflect"

	"google.golang.org/protobuf/proto"
)

func newProtoMessage[T proto.Message](template T) (T, error) {
	var zero T
	if isNilProtoMessage(template) {
		return zero, ErrMessageMissing
	}
	typ := reflect.TypeOf(template)
	if typ.Kind() != reflect.Pointer {
		return zero, ErrMessageMissing
	}
	msg, ok := reflect.New(typ.Elem()).Interface().(T)
	if !ok {
		return zero, ErrMessageMissing
	}
	return msg, nil
}

func cloneProtoMessage[T proto.Message](msg T) T {
	var zero T
	if isNilProtoMessage(msg) {
		return zero
	}
	cloned, ok := proto.Clone(msg).(T)
	if !ok {
		return zero
	}
	return cloned
}

func isNilProtoMessage[T proto.Message](msg T) bool {
	value := reflect.ValueOf(msg)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
