package jsonl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// Sink appends observe events to a local JSONL file.
type Sink struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// New creates a local JSONL event sink.
func New(path string) (*Sink, error) {
	if path == "" {
		return nil, fmt.Errorf("observe jsonl path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("observe jsonl create dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("observe jsonl open file: %w", err)
	}
	return &Sink{file: file, enc: json.NewEncoder(file)}, nil
}

// Write appends events as one JSON object per line.
func (s *Sink) Write(ctx context.Context, events []contracts.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.file == nil || s.enc == nil {
		return fmt.Errorf("observe jsonl sink is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		if err := s.enc.Encode(event.Clone()); err != nil {
			return fmt.Errorf("observe jsonl encode event: %w", err)
		}
	}
	return nil
}

// Close closes the underlying file.
func (s *Sink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.enc = nil
	return err
}
