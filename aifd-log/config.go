package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	// SecondaryHandler receives best-effort copies after the local write succeeds.
	// Adapters and their lifecycle are owned by the host, not this package.
	SecondaryHandler slog.Handler
	Rotation         RotationConfig
	// Stat enables typed CSV output owned by InitDefault. Nil disables typed Stat.
	Stat *StatConfig
}

// InitDefault creates a local-first logger and installs it as the process default.
func InitDefault(cfg Config) (CloseFunc, error) {
	cfg = cfg.withDefaults()
	if cfg.Stat != nil {
		local, err := filepath.Abs(cfg.LocalPath)
		if err != nil {
			return nil, err
		}
		stat, err := filepath.Abs(cfg.Stat.LocalPath)
		if err != nil {
			return nil, err
		}
		if local == stat || (runtime.GOOS == "windows" && strings.EqualFold(local, stat)) {
			return nil, fmt.Errorf("ordinary logs and Stat require different local files")
		}
	}
	localCfg := cfg
	localCfg.Stat = nil
	logger, closeFn, err := NewLocalFirstLogger(localCfg)
	if err != nil {
		return nil, err
	}
	state := emptyStatState()
	state.logger = logger
	if cfg.Stat != nil {
		state.recorder, err = newStatRecorder(*cfg.Stat, state.reportError)
		if err != nil {
			_ = closeFn()
			return nil, err
		}
	}
	defaultMu.Lock()
	defaultLogger, defaultStat = logger, state
	defaultMu.Unlock()
	var once sync.Once
	var closeErr error
	return func() error {
		once.Do(func() {
			if state.recorder != nil {
				closeErr = state.recorder.close()
			}
			closeErr = errors.Join(closeErr, closeFn())
		})
		return closeErr
	}, nil
}

// NewLocalFirstLogger creates a logger whose required path is local text output.
func NewLocalFirstLogger(cfg Config) (Logger, CloseFunc, error) {
	if cfg.Stat != nil {
		return nil, nil, fmt.Errorf("typed Stat configuration requires InitDefault")
	}
	cfg = cfg.withDefaults()
	local, closeFn, err := newLocalHandler(cfg)
	if err != nil {
		return nil, nil, err
	}
	logger, err := NewTeeLogger(local, cfg.SecondaryHandler)
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
	file, err := openLocalFile(cfg.LocalPath, cfg.Rotation)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	writer := io.Writer(file)
	if cfg.AlsoStdout {
		writer = io.MultiWriter(file, os.Stdout)
	}
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.Level(cfg.Level)})
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
