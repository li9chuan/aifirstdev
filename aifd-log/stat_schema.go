package logging

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// StatSchema binds one payload struct to an application-owned event identity.
// Name and Version are copied at registration; the framework does not infer upgrades.
type StatSchema struct {
	Name    string
	Version int
	typ     reflect.Type
	layout  *statLayout
}

// StatEventProvider lets an existing runtime module declare its typed events.
// StatEvents must only return definitions, without performing registration or I/O.
type StatEventProvider interface {
	StatEvents() []StatSchema
}

type statKey struct {
	name    string
	version int
}

type statField struct {
	index int
	name  string
}

type statLayout struct {
	typ    reflect.Type
	fields []statField
}

// DefineStat constructs a definition without registration or other side effects.
// Invalid definitions are returned as errors by runtime registration, not panics here.
func DefineStat[T any](name string, version int) StatSchema {
	return StatSchema{Name: name, Version: version, typ: reflect.TypeFor[T]()}
}

// RegisterStatEvents validates a batch before publishing it to the process logger.
// Identical registrations are idempotent. Call after InitDefault and before traffic.
func RegisterStatEvents(events ...StatSchema) error {
	if len(events) == 0 {
		return nil
	}
	prepared := make([]StatSchema, len(events))
	for i, event := range events {
		if event.Name == "" || strings.TrimSpace(event.Name) != event.Name || event.Version < 1 {
			return fmt.Errorf("stat event requires a nonempty name and positive version: %q/%d", event.Name, event.Version)
		}
		layout, err := newStatLayout(event.typ)
		if err != nil {
			return fmt.Errorf("stat event %s/%d: %w", event.Name, event.Version, err)
		}
		event.typ, event.layout = layout.typ, layout
		prepared[i] = event
	}
	s := currentStat()
	s.mu.Lock()
	defer s.mu.Unlock()
	byType := make(map[reflect.Type]StatSchema, len(s.events)+len(prepared))
	byKey := make(map[statKey]reflect.Type, len(s.keys)+len(prepared))
	for typ, event := range s.events {
		byType[typ] = event
	}
	for key, typ := range s.keys {
		byKey[key] = typ
	}
	for _, event := range prepared {
		key := statKey{event.Name, event.Version}
		if previous, ok := byType[event.typ]; ok && (previous.Name != event.Name || previous.Version != event.Version) {
			return fmt.Errorf("stat payload %s already binds %s/%d", event.typ, previous.Name, previous.Version)
		}
		if previous, ok := byKey[key]; ok && previous != event.typ {
			return fmt.Errorf("stat event %s/%d already binds %s", event.Name, event.Version, previous)
		}
		byType[event.typ], byKey[key] = event, event.typ
	}
	s.events, s.keys = byType, byKey
	return nil
}

// RegisterStatHeader validates a concrete header and its runtime's event shapes.
// Unrelated service events may use different headers. There are no framework
// header names, versions or mandatory business columns.
func RegisterStatHeader[H any](events ...StatSchema) error {
	header, err := currentStat().headerLayout(reflect.TypeFor[H]())
	if err != nil {
		return err
	}
	for _, event := range events {
		layout, err := newStatLayout(event.typ)
		if err != nil {
			return err
		}
		if err := validateStatColumns(header, layout); err != nil {
			return fmt.Errorf("stat header for %s/%d: %w", event.Name, event.Version, err)
		}
	}
	return nil
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
