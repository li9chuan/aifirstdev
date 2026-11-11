package logging

import (
	"context"
	"encoding/csv"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StatCounters describes the current process Stat output, not business results.
type StatCounters struct {
	LocalWritten   uint64
	RecordErrors   uint64
	Forwarded      uint64
	ForwardErrors  uint64
	ForwardDropped uint64
}

// statState owns definitions and header codecs for one InitDefault lifetime.
// They are discovered from event types on use, never reconstructed from log files.
type statState struct {
	mu           sync.RWMutex
	events       map[reflect.Type]statDefinition
	names        map[string]reflect.Type
	headers      map[reflect.Type]*statLayout
	recorder     *statRecorder
	logger       Logger
	recordErrors atomic.Uint64
	lastError    atomic.Int64
}

var defaultStat = emptyStatState()

func emptyStatState() *statState {
	return &statState{
		events:  make(map[reflect.Type]statDefinition),
		names:   make(map[string]reflect.Type),
		headers: make(map[reflect.Type]*statLayout),
	}
}

func currentStat() *statState {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultStat
}

// RecordStat accepts a named struct or a non-nil pointer to one. Its exact type
// name is the event name; distinct types cannot share a name within one logger.
// It validates/caches the layout on first use, then builds the application header
// and encodes both synchronously. Only encoded text can enter forwarding.
// Disabled Stat skips validation and buildHeader. Errors (including a header
// callback panic) are counted and logged, and returned
// for adapters that need them; ordinary User.Stat calls do not fail business work.
func RecordStat(payload any, buildHeader func(eventName string) any) (err error) {
	s := currentStat()
	if s.recorder == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("stat header or encoding panic: %v", recovered)
		}
		if err != nil {
			s.recordErrors.Add(1)
			s.reportError(err)
		}
	}()
	value, err := statValue(payload)
	if err != nil {
		return err
	}
	event, err := s.eventDefinition(value.Type())
	if err != nil {
		return err
	}
	if buildHeader == nil {
		return fmt.Errorf("stat header builder is not configured")
	}
	header, err := statValue(buildHeader(event.name))
	if err != nil {
		return fmt.Errorf("stat header: %w", err)
	}
	layout, err := s.headerLayout(header.Type())
	if err != nil {
		return err
	}
	if err := validateStatColumns(layout, event.layout); err != nil {
		return err
	}
	row := make([]string, 0, len(layout.fields)+len(event.layout.fields))
	row = layout.appendValues(row, header)
	row = event.layout.appendValues(row, value)
	if len(row) == 1 && row[0] == "" {
		// encoding/csv emits an empty physical line here, which readers skip.
		return s.recorder.write("\"\"\n")
	}
	var record strings.Builder
	writer := csv.NewWriter(&record)
	if err := writer.Write(row); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return s.recorder.write(record.String())
}

// GetStatCounters returns a race-safe snapshot of the current logger's counters.
// Reinitializing the process logger starts a new definition/counter lifetime.
func GetStatCounters() StatCounters {
	s := currentStat()
	result := StatCounters{RecordErrors: s.recordErrors.Load()}
	if r := s.recorder; r != nil {
		result.LocalWritten = r.localWritten.Load()
		result.Forwarded = r.forwarded.Load()
		result.ForwardErrors = r.forwardErrors.Load()
		result.ForwardDropped = r.forwardDropped.Load()
	}
	return result
}

func (s *statState) reportError(err error) {
	// A disconnected receiver must not produce an error log for every business event.
	now := time.Now().UnixNano()
	previous := s.lastError.Load()
	if previous != 0 && now-previous < int64(time.Second) {
		return
	}
	if s.lastError.CompareAndSwap(previous, now) {
		logger := s.logger
		if logger == nil {
			logger = Default()
		}
		logger.Error(context.Background(), "stat output failed", Err(err))
	}
}
