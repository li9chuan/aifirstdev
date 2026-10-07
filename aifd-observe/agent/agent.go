package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

const (
	defaultMaxLines     = 200
	defaultMaxBytes     = int64(1024 * 1024)
	defaultQueryTimeout = 2 * time.Second
	defaultConcurrency  = 1
)

// ErrQueryConcurrencyLimit is returned when local file queries are saturated.
var ErrQueryConcurrencyLimit = errors.New("observe agent query concurrency limit reached")

// Flusher is implemented by optional sinks that can flush buffered events.
type Flusher interface {
	Flush(context.Context) error
}

// File describes one local observe file that can be queried by the agent.
type File struct {
	Path string
	Kind contracts.Kind
}

// Config configures the local observe agent.
type Config struct {
	Service        string
	Instance       string
	Files          map[string]File
	DefaultFile    string
	MaxLines       int
	MaxBytes       int64
	QueryTimeout   time.Duration
	MaxConcurrency int
	CollectRuntime bool
	Flusher        Flusher
	Now            func() time.Time
}

// Agent provides local log/stat file queries and lightweight health state.
type Agent struct {
	service        string
	instance       string
	files          map[string]File
	defaultFile    string
	fileDirs       []string
	maxLines       int
	maxBytes       int64
	queryTimeout   time.Duration
	maxConcurrency int
	queryTokens    chan struct{}
	collectRuntime bool
	flusher        Flusher
	now            func() time.Time
	startedAt      time.Time

	mu              sync.Mutex
	queryCount      uint64
	activeQueries   uint64
	rejectedQueries uint64
	received        uint64
	dropped         uint64
	byKind          map[contracts.Kind]uint64
	lastErr         string
	closed          bool
}

// New creates a local observe agent.
func New(cfg Config) (*Agent, error) {
	if cfg.MaxLines <= 0 {
		cfg.MaxLines = defaultMaxLines
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = defaultQueryTimeout
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = defaultConcurrency
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	files := make(map[string]File, len(cfg.Files))
	for name, file := range cfg.Files {
		if name == "" {
			return nil, fmt.Errorf("observe agent file name is empty")
		}
		if file.Path == "" {
			return nil, fmt.Errorf("observe agent file %s path is empty", name)
		}
		abs, err := filepath.Abs(file.Path)
		if err != nil {
			return nil, fmt.Errorf("observe agent resolve file %s: %w", name, err)
		}
		file.Path = filepath.Clean(abs)
		if file.Kind == "" {
			file.Kind = contracts.KindLog
		}
		files[name] = file
	}
	fileDirs := deriveFileDirs(files)
	defaultFile := cfg.DefaultFile
	if defaultFile == "" && len(files) == 1 {
		for name := range files {
			defaultFile = name
		}
	}
	if defaultFile != "" {
		if _, ok := files[defaultFile]; !ok {
			return nil, fmt.Errorf("observe agent default file %s is not configured", defaultFile)
		}
	}
	return &Agent{
		service:        cfg.Service,
		instance:       cfg.Instance,
		files:          files,
		defaultFile:    defaultFile,
		fileDirs:       fileDirs,
		maxLines:       cfg.MaxLines,
		maxBytes:       cfg.MaxBytes,
		queryTimeout:   cfg.QueryTimeout,
		maxConcurrency: cfg.MaxConcurrency,
		queryTokens:    make(chan struct{}, cfg.MaxConcurrency),
		collectRuntime: cfg.CollectRuntime,
		flusher:        cfg.Flusher,
		now:            cfg.Now,
		startedAt:      cfg.Now(),
		byKind:         make(map[contracts.Kind]uint64),
	}, nil
}

// Write counts received events; logs remain queryable from local files.
func (a *Agent) Write(_ context.Context, events []contracts.Event) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		a.dropped += uint64(len(events))
		return nil
	}
	for _, event := range events {
		a.received++
		a.byKind[event.Kind]++
	}
	return nil
}

// Close marks the agent closed.
func (a *Agent) Close(context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return nil
}

// Flush forwards to the configured flusher when one exists.
func (a *Agent) Flush(ctx context.Context) error {
	if a == nil || a.flusher == nil {
		return nil
	}
	return a.flusher.Flush(ctx)
}

func deriveFileDirs(files map[string]File) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(files))
	for _, file := range files {
		dir := filepath.Clean(filepath.Dir(file.Path))
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func withinFileDirs(path string, fileDirs []string) bool {
	for _, dir := range fileDirs {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// TailQuery describes a local log file query.
type TailQuery struct {
	File     string
	Kind     contracts.Kind
	Level    contracts.Level
	Name     string
	Contains string
	Since    time.Time
	Limit    int
	MaxBytes int64
}

// TailEntry is one matching line returned from a local observe file.
type TailEntry struct {
	File    string
	Path    string
	Text    string
	Time    time.Time
	Kind    contracts.Kind
	Level   contracts.Level
	Name    string
	Message string
}

// TailResult is the result of a local observe file query.
type TailResult struct {
	Entries   []TailEntry
	Truncated bool
}

// Tail searches the configured local observe file.
func (a *Agent) Tail(ctx context.Context, query TailQuery) (TailResult, error) {
	if a == nil {
		return TailResult{}, fmt.Errorf("observe agent is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := a.queryTimeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := a.beginQuery(); err != nil {
		a.recordRejectedQuery(err)
		return TailResult{}, err
	}
	var result TailResult
	var err error
	defer func() {
		a.finishQuery(err)
	}()
	result, err = a.tail(ctx, query)
	return result, err
}

func (a *Agent) beginQuery() error {
	select {
	case a.queryTokens <- struct{}{}:
		a.mu.Lock()
		a.activeQueries++
		a.mu.Unlock()
		return nil
	default:
		return ErrQueryConcurrencyLimit
	}
}

func (a *Agent) finishQuery(err error) {
	<-a.queryTokens
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queryCount++
	if a.activeQueries > 0 {
		a.activeQueries--
	}
	if err != nil {
		a.lastErr = err.Error()
	}
}

func (a *Agent) recordRejectedQuery(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queryCount++
	a.rejectedQueries++
	if err != nil {
		a.lastErr = err.Error()
	}
}

func (a *Agent) tail(ctx context.Context, query TailQuery) (TailResult, error) {
	fileName := query.File
	if fileName == "" {
		fileName = a.defaultFile
	}
	if fileName == "" {
		return TailResult{}, fmt.Errorf("observe agent default file is not configured")
	}
	file, ok := a.files[fileName]
	if !ok {
		return TailResult{}, fmt.Errorf("observe agent file %s is not configured", fileName)
	}
	if !withinFileDirs(file.Path, a.fileDirs) {
		return TailResult{}, fmt.Errorf("observe agent file %s is outside registered file dirs", fileName)
	}
	limit := query.Limit
	if limit <= 0 || limit > a.maxLines {
		limit = a.maxLines
	}
	maxBytes := query.MaxBytes
	if maxBytes <= 0 || maxBytes > a.maxBytes {
		maxBytes = a.maxBytes
	}
	raw, truncated, err := readTailBytes(ctx, file.Path, maxBytes)
	if err != nil {
		return TailResult{}, err
	}
	entries, err := filterLines(ctx, fileName, file, raw, truncated, query, limit)
	if err != nil {
		return TailResult{}, err
	}
	return TailResult{Entries: entries, Truncated: truncated}, nil
}

func readTailBytes(ctx context.Context, path string, maxBytes int64) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("observe agent open file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("observe agent stat file: %w", err)
	}
	size := info.Size()
	start := int64(0)
	truncated := false
	if size > maxBytes {
		start = size - maxBytes
		truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, false, fmt.Errorf("observe agent seek file: %w", err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, false, fmt.Errorf("observe agent read file: %w", err)
	}
	if truncated {
		if idx := bytes.IndexByte(raw, '\n'); idx >= 0 {
			raw = raw[idx+1:]
		}
	}
	return raw, truncated, nil
}

func filterLines(ctx context.Context, fileName string, file File, raw []byte, truncated bool, query TailQuery, limit int) ([]TailEntry, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), int(defaultMaxBytes))
	var entries []TailEntry
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text := scanner.Text()
		if query.Contains != "" && !strings.Contains(text, query.Contains) {
			continue
		}
		entry := parseEntry(fileName, file, text)
		if query.Kind != "" && entry.Kind != query.Kind {
			continue
		}
		if query.Level != "" && entry.Level != query.Level {
			continue
		}
		if query.Name != "" && entry.Name != query.Name {
			continue
		}
		if !query.Since.IsZero() && !entry.Time.IsZero() && entry.Time.Before(query.Since) {
			continue
		}
		if len(entries) == limit {
			copy(entries, entries[1:])
			entries[len(entries)-1] = entry
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("observe agent scan file: %w", err)
	}
	_ = truncated
	return entries, nil
}

func parseEntry(fileName string, file File, text string) TailEntry {
	entry := TailEntry{File: fileName, Path: file.Path, Text: text, Kind: file.Kind}
	var fields map[string]any
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return entry
	}
	entry.Time = parseTimeField(fields["time"], fields["Time"])
	entry.Level = normalizeLevel(firstString(fields["level"], fields["Level"]))
	entry.Name = firstString(fields["name"], fields["Name"], fields["module"])
	entry.Message = firstString(fields["msg"], fields["message"], fields["Message"])
	if kind := firstString(fields["kind"], fields["Kind"]); kind != "" {
		entry.Kind = contracts.Kind(strings.ToLower(kind))
	}
	return entry
}

func firstString(values ...any) string {
	for _, value := range values {
		if s, ok := value.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func parseTimeField(values ...any) time.Time {
	for _, value := range values {
		s, ok := value.(string)
		if !ok || s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func normalizeLevel(level string) contracts.Level {
	switch strings.ToLower(level) {
	case "debug":
		return contracts.LevelDebug
	case "info":
		return contracts.LevelInfo
	case "warn", "warning":
		return contracts.LevelWarn
	case "error":
		return contracts.LevelError
	default:
		return contracts.Level(level)
	}
}

// RuntimeStats contains optional Go runtime metrics.
type RuntimeStats struct {
	Goroutines uint64
	AllocBytes uint64
	SysBytes   uint64
	NumGC      uint32
}

// Stats describes the local agent state.
type Stats struct {
	Service         string
	Instance        string
	StartedAt       time.Time
	Uptime          time.Duration
	Queries         uint64
	ActiveQueries   uint64
	RejectedQueries uint64
	Received        uint64
	Dropped         uint64
	ByKind          map[contracts.Kind]uint64
	LastError       string
	Runtime         RuntimeStats
}

// Stats returns a snapshot of the local agent state.
func (a *Agent) Stats() Stats {
	if a == nil {
		return Stats{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := Stats{
		Service:         a.service,
		Instance:        a.instance,
		StartedAt:       a.startedAt,
		Uptime:          a.now().Sub(a.startedAt),
		Queries:         a.queryCount,
		ActiveQueries:   a.activeQueries,
		RejectedQueries: a.rejectedQueries,
		Received:        a.received,
		Dropped:         a.dropped,
		LastError:       a.lastErr,
		ByKind:          make(map[contracts.Kind]uint64, len(a.byKind)),
	}
	for kind, count := range a.byKind {
		out.ByKind[kind] = count
	}
	if a.collectRuntime {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		out.Runtime = RuntimeStats{
			Goroutines: uint64(runtime.NumGoroutine()),
			AllocBytes: mem.Alloc,
			SysBytes:   mem.Sys,
			NumGC:      mem.NumGC,
		}
	}
	return out
}

// FileHealth describes one configured file's readability.
type FileHealth struct {
	Name     string
	Path     string
	Kind     contracts.Kind
	Exists   bool
	Readable bool
	Error    string
}

// Health describes whether local files can be queried.
type Health struct {
	OK        bool
	Files     []FileHealth
	LastError string
}

// Health returns configured file readability state.
func (a *Agent) Health() Health {
	if a == nil {
		return Health{OK: false, LastError: "observe agent is nil"}
	}
	stats := a.Stats()
	health := Health{OK: true, LastError: stats.LastError}
	names := make([]string, 0, len(a.files))
	for name := range a.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		file := a.files[name]
		item := FileHealth{Name: name, Path: file.Path, Kind: file.Kind}
		if !withinFileDirs(file.Path, a.fileDirs) {
			item.Error = "outside registered file dirs"
			health.OK = false
			health.Files = append(health.Files, item)
			continue
		}
		f, err := os.Open(file.Path)
		if err != nil {
			item.Error = err.Error()
			health.OK = false
		} else {
			item.Exists = true
			item.Readable = true
			_ = f.Close()
		}
		health.Files = append(health.Files, item)
	}
	return health
}
