package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

const (
	defaultServiceName = "aifd"
	defaultLogDir      = "run/logs"
)

// CloseFunc closes resources owned by a logger.
type CloseFunc func() error

// Config controls local-first logging initialization.
type Config struct {
	LocalPath      string
	AlsoStdout     bool
	Level          Level
	SourceService  string
	SourceInstance string
	Observe        contracts.Sink
}

// InitDefault creates a local-first logger and installs it as the process default.
func InitDefault(cfg Config) (CloseFunc, error) {
	cfg = cfg.withDefaults()
	logger, closeFn, err := NewLocalFirstLogger(cfg)
	if err != nil {
		return nil, err
	}
	SetDefault(logger)
	SetDefaultSink(cfg.Observe)
	setDefaultSource(cfg.SourceService, cfg.SourceInstance)
	return closeFn, nil
}

// NewLocalFirstLogger creates a logger whose required path is local JSONL output.
func NewLocalFirstLogger(cfg Config) (Logger, CloseFunc, error) {
	cfg = cfg.withDefaults()
	local, closeFn, err := newLocalHandler(cfg)
	if err != nil {
		return nil, nil, err
	}
	var observe slog.Handler
	if cfg.Observe != nil {
		observe = NewSinkHandler(cfg.Observe)
	}
	logger, err := NewTeeLogger(local, observe)
	if err != nil {
		_ = closeFn()
		return nil, nil, err
	}
	logger = logger.With(
		String("source_service", cfg.SourceService),
		String("source_instance", cfg.SourceInstance),
	)
	return logger, closeFn, nil
}

func (cfg Config) withDefaults() Config {
	if cfg.SourceService == "" {
		cfg.SourceService = defaultServiceName
	}
	if cfg.SourceInstance == "" {
		if hostname, err := os.Hostname(); err == nil {
			cfg.SourceInstance = hostname
		}
	}
	if cfg.LocalPath == "" {
		cfg.LocalPath = filepath.Join(defaultLogDir, sanitizePathPart(cfg.SourceService)+".log")
	}
	return cfg
}

func newLocalHandler(cfg Config) (slog.Handler, CloseFunc, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.LocalPath), 0755); err != nil {
		return nil, nil, fmt.Errorf("logging create local log dir: %w", err)
	}
	file, err := os.OpenFile(cfg.LocalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("logging open local log file: %w", err)
	}
	writer := io.Writer(file)
	if cfg.AlsoStdout {
		writer = io.MultiWriter(file, os.Stdout)
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.Level(cfg.Level)})
	return handler, file.Close, nil
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultServiceName
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, value)
}
