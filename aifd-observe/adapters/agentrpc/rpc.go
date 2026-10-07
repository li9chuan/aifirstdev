package agentrpc

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/agent"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	// TopicTail queries local observe files.
	TopicTail = "observe.tail"
	// TopicStats queries local agent counters and runtime stats.
	TopicStats = "observe.stats"
	// TopicHealth queries local agent health.
	TopicHealth = "observe.health"
	// TopicFlush flushes configured observe buffers when supported.
	TopicFlush = "observe.flush"
)

// LocalAgent is the local control-plane surface exposed through observe RPC.
type LocalAgent interface {
	Tail(context.Context, agent.TailQuery) (agent.TailResult, error)
	Stats() agent.Stats
	Health() agent.Health
	Flush(context.Context) error
}

// Register installs observe agent admin RPC routes into reg.
func Register(reg *rpc.Registry, local LocalAgent) error {
	if isNilLocalAgent(local) {
		return fmt.Errorf("observe agent is nil")
	}
	if err := rpc.Admin(reg, TopicTail, func(s *rpc.Session, req *structpb.Struct) {
		result, err := local.Tail(s.Context(), tailQueryFromStruct(req))
		if err != nil {
			s.Error(rpc.CodeInvalid, err.Error())
			return
		}
		respondStruct(s, TopicTail+".rsp", tailResultMap(result))
	}); err != nil {
		return err
	}
	if err := rpc.Admin(reg, TopicStats, func(s *rpc.Session, _ *structpb.Struct) {
		respondStruct(s, TopicStats+".rsp", statsMap(local.Stats()))
	}); err != nil {
		return err
	}
	if err := rpc.Admin(reg, TopicHealth, func(s *rpc.Session, _ *structpb.Struct) {
		respondStruct(s, TopicHealth+".rsp", healthMap(local.Health()))
	}); err != nil {
		return err
	}
	if err := rpc.Admin(reg, TopicFlush, func(s *rpc.Session, _ *structpb.Struct) {
		if err := local.Flush(s.Context()); err != nil {
			s.Error(rpc.CodeInternal, err.Error())
			return
		}
		respondStruct(s, TopicFlush+".rsp", map[string]any{"ok": true})
	}); err != nil {
		return err
	}
	return nil
}

func isNilLocalAgent(local LocalAgent) bool {
	if local == nil {
		return true
	}
	value := reflect.ValueOf(local)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func respondStruct(s *rpc.Session, topic string, fields map[string]any) {
	pb, err := structpb.NewStruct(fields)
	if err != nil {
		s.Error(rpc.CodeInternal, err.Error())
		return
	}
	s.ResponseAs(topic, pb)
}

func tailQueryFromStruct(req *structpb.Struct) agent.TailQuery {
	if req == nil {
		return agent.TailQuery{}
	}
	fields := req.AsMap()
	query := agent.TailQuery{
		File:     stringValue(fields["file"]),
		Kind:     contracts.Kind(stringValue(fields["kind"])),
		Level:    contracts.Level(stringValue(fields["level"])),
		Name:     stringValue(fields["name"]),
		Contains: stringValue(fields["contains"]),
		Limit:    intValue(fields["limit"]),
		MaxBytes: int64Value(fields["max_bytes"]),
	}
	if since := stringValue(fields["since"]); since != "" {
		if t, err := time.Parse(time.RFC3339Nano, since); err == nil {
			query.Since = t
		}
	}
	return query
}

func tailResultMap(result agent.TailResult) map[string]any {
	entries := make([]any, 0, len(result.Entries))
	for _, entry := range result.Entries {
		item := map[string]any{
			"file":    entry.File,
			"path":    entry.Path,
			"text":    entry.Text,
			"kind":    string(entry.Kind),
			"level":   string(entry.Level),
			"name":    entry.Name,
			"message": entry.Message,
		}
		if !entry.Time.IsZero() {
			item["time"] = entry.Time.Format(time.RFC3339Nano)
		}
		entries = append(entries, item)
	}
	return map[string]any{
		"entries":   entries,
		"truncated": result.Truncated,
	}
}

func statsMap(stats agent.Stats) map[string]any {
	byKind := make(map[string]any, len(stats.ByKind))
	for kind, count := range stats.ByKind {
		byKind[string(kind)] = float64(count)
	}
	return map[string]any{
		"service":          stats.Service,
		"instance":         stats.Instance,
		"started_at":       stats.StartedAt.Format(time.RFC3339Nano),
		"uptime_ms":        float64(stats.Uptime.Milliseconds()),
		"queries":          float64(stats.Queries),
		"active_queries":   float64(stats.ActiveQueries),
		"rejected_queries": float64(stats.RejectedQueries),
		"received":         float64(stats.Received),
		"dropped":          float64(stats.Dropped),
		"by_kind":          byKind,
		"last_error":       stats.LastError,
		"runtime": map[string]any{
			"goroutines":  float64(stats.Runtime.Goroutines),
			"alloc_bytes": float64(stats.Runtime.AllocBytes),
			"sys_bytes":   float64(stats.Runtime.SysBytes),
			"num_gc":      float64(stats.Runtime.NumGC),
		},
	}
}

func healthMap(health agent.Health) map[string]any {
	files := make([]any, 0, len(health.Files))
	for _, file := range health.Files {
		files = append(files, map[string]any{
			"name":     file.Name,
			"path":     file.Path,
			"kind":     string(file.Kind),
			"exists":   file.Exists,
			"readable": file.Readable,
			"error":    file.Error,
		})
	}
	return map[string]any{
		"ok":         health.OK,
		"files":      files,
		"last_error": health.LastError,
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// Flush calls the local agent flusher. It is exported for direct tests and tools.
func Flush(ctx context.Context, local *agent.Agent) error {
	if local == nil {
		return fmt.Errorf("observe agent is nil")
	}
	return local.Flush(ctx)
}
