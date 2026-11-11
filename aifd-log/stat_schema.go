package logging

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type statDefinition struct {
	name   string
	layout *statLayout
}

type statField struct {
	index int
	name  string
}

type statLayout struct {
	typ    reflect.Type
	fields []statField
}

// ValidateStatHeader checks the application header's fields without registering
// events or invoking a header builder. Header/event collisions are checked on use.
func ValidateStatHeader[H any]() error {
	_, err := newStatLayout(reflect.TypeFor[H]())
	return err
}

// eventDefinition receives the non-pointer struct type resolved by statValue.
// A name belongs to one exact Go type for this logger's lifetime, across packages.
func (s *statState) eventDefinition(typ reflect.Type) (statDefinition, error) {
	s.mu.RLock()
	cached, ok := s.events[typ]
	s.mu.RUnlock()
	if ok {
		return cached, nil
	}
	name := typ.Name()
	if name == "" {
		return statDefinition{}, fmt.Errorf("stat event requires a named struct, got %s", typ)
	}
	layout, err := newStatLayout(typ)
	if err != nil {
		return statDefinition{}, fmt.Errorf("stat event %s: %w", name, err)
	}
	definition := statDefinition{name: name, layout: layout}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.events[typ]; ok {
		return previous, nil
	}
	if previous, ok := s.names[name]; ok && previous != typ {
		return statDefinition{}, fmt.Errorf("stat event %q from %s (%s) already belongs to %s (%s)", name, typ, typ.PkgPath(), previous, previous.PkgPath())
	}
	s.events[typ], s.names[name] = definition, typ
	return definition, nil
}

func (s *statState) headerLayout(typ reflect.Type) (*statLayout, error) {
	key := typ
	if key != nil && key.Kind() == reflect.Pointer {
		key = key.Elem()
	}
	s.mu.RLock()
	cached := s.headers[key]
	s.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	layout, err := newStatLayout(typ)
	if err != nil {
		return nil, fmt.Errorf("stat header: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous := s.headers[layout.typ]; previous != nil {
		return previous, nil
	}
	s.headers[layout.typ] = layout
	return layout, nil
}

func newStatLayout(typ reflect.Type) (*statLayout, error) {
	if typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected a struct or struct pointer, got %v", typ)
	}
	layout := &statLayout{typ: typ}
	names := make(map[string]bool)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Tag.Get("stat")
		if name == "-" {
			continue
		}
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, ",\r\n") {
			return nil, fmt.Errorf("field %s.%s requires a plain stat tag", typ, field.Name)
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate stat column %q", name)
		}
		if !isStatScalar(field.Type) {
			return nil, fmt.Errorf("unsupported stat field %s.%s (%s)", typ, field.Name, field.Type)
		}
		names[name] = true
		layout.fields = append(layout.fields, statField{index: i, name: name})
	}
	return layout, nil
}

func validateStatColumns(header, event *statLayout) error {
	if len(header.fields)+len(event.fields) == 0 {
		return fmt.Errorf("stat record requires at least one column")
	}
	for _, left := range header.fields {
		for _, right := range event.fields {
			if left.name == right.name {
				return fmt.Errorf("header and event repeat stat column %q", left.name)
			}
		}
	}
	return nil
}

func isStatScalar(typ reflect.Type) bool {
	if typ == reflect.TypeFor[time.Time]() {
		return true
	}
	switch typ.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func statValue(value any) (reflect.Value, error) {
	v := reflect.ValueOf(value)
	if v.IsValid() && v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("stat value must be a non-nil struct or struct pointer")
	}
	return v, nil
}

func (layout *statLayout) appendValues(row []string, value reflect.Value) []string {
	for _, field := range layout.fields {
		v := value.Field(field.index)
		var text string
		if v.Type() == reflect.TypeFor[time.Time]() {
			text = v.Interface().(time.Time).Format(time.RFC3339Nano)
		} else {
			switch v.Kind() {
			case reflect.Bool:
				text = strconv.FormatBool(v.Bool())
			case reflect.String:
				text = v.String()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				text = strconv.FormatInt(v.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				text = strconv.FormatUint(v.Uint(), 10)
			case reflect.Float32, reflect.Float64:
				text = strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits())
			}
		}
		row = append(row, text)
	}
	return row
}
