package rdb

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultFailureFileDir = "run/rdb-failed"
	journalSourceClose    = "close"
)

type journalRecord struct {
	Source      string        `json:"source"`
	Kind        string        `json:"kind"`
	Intent      string        `json:"intent"`
	Key         string        `json:"key"`
	Field       string        `json:"field,omitempty"`
	ValueBase64 string        `json:"valueBase64,omitempty"`
	TTL         time.Duration `json:"ttl,omitempty"`
	Attempts    int           `json:"attempts,omitempty"`
	Err         string        `json:"err,omitempty"`
	Ts          int64         `json:"ts"`
}

func writeJournalFile(dir, source string, records []journalRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	base := fmt.Sprintf("rdb-%s-%d-%d.jsonl", source, time.Now().UnixNano(), os.Getpid())
	finalPath := filepath.Join(dir, base)
	tmpPath := finalPath + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			return err
		}
		if _, err := writer.Write(line); err != nil {
			_ = file.Close()
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

func replayStartupJournals(ctx context.Context, store store, dir string) error {
	if dir == "" {
		dir = defaultFailureFileDir
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".replaying") {
			continue
		}
		return fmt.Errorf("%w: %s", ErrJournalReplayInProgress, filepath.Join(dir, entry.Name()))
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := replayJournalFile(ctx, store, path); err != nil {
			return err
		}
	}
	return nil
}

func replayJournalFile(ctx context.Context, store store, path string) error {
	replayingPath := strings.TrimSuffix(path, ".jsonl") + ".replaying"
	if err := os.Rename(path, replayingPath); err != nil {
		return err
	}
	records, err := readJournalRecords(replayingPath)
	if err != nil {
		return err
	}
	closeRecords := records[:0]
	for _, record := range records {
		if record.Source == journalSourceClose {
			closeRecords = append(closeRecords, record)
		}
	}
	if len(closeRecords) == 0 {
		return os.Rename(replayingPath, path)
	}
	if err := applyJournalRecords(ctx, store, closeRecords); err != nil {
		_ = os.Rename(replayingPath, path)
		return err
	}
	donePath := strings.TrimSuffix(replayingPath, ".replaying") + ".done"
	return os.Rename(replayingPath, donePath)
}

func readJournalRecords(path string) ([]journalRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []journalRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record journalRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func applyJournalRecords(ctx context.Context, store store, records []journalRecord) error {
	hashTTL := buildHashTTLPlan(records)
	for _, record := range records {
		if err := record.applyReplay(ctx, store); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(hashTTL))
	for key := range hashTTL {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ttl := hashTTL[key]
		if ttl > 0 {
			if err := store.Expire(ctx, key, ttl); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildHashTTLPlan(records []journalRecord) map[string]time.Duration {
	type hashInfo struct {
		hasDelete bool
		maxTTL    time.Duration
	}
	infoByKey := make(map[string]hashInfo)
	for _, record := range records {
		if record.Kind != intentKindHash {
			continue
		}
		info := infoByKey[record.Key]
		if record.Intent == intentDelete {
			info.hasDelete = true
		}
		if record.Intent == intentSave && record.TTL > info.maxTTL {
			info.maxTTL = record.TTL
		}
		infoByKey[record.Key] = info
	}
	out := make(map[string]time.Duration)
	for key, info := range infoByKey {
		if !info.hasDelete && info.maxTTL > 0 {
			out[key] = info.maxTTL
		}
	}
	return out
}

func (r journalRecord) applyLive(ctx context.Context, store store) error {
	_, err := r.applyLiveMeasured(ctx, store)
	return err
}

func (r journalRecord) applyLiveMeasured(ctx context.Context, store store) (int, error) {
	switch {
	case r.Kind == intentKindSet && r.Intent == intentSave:
		value, err := r.value()
		if err != nil {
			return 0, err
		}
		return len(value), store.Set(ctx, r.Key, value, r.TTL)
	case r.Kind == intentKindSet && r.Intent == intentDelete:
		return 0, store.Delete(ctx, r.Key)
	case r.Kind == intentKindHash && r.Intent == intentSave:
		value, err := r.value()
		if err != nil {
			return 0, err
		}
		if err := store.HSet(ctx, r.Key, r.Field, value); err != nil {
			return len(value), err
		}
		if r.TTL > 0 {
			return len(value), store.Expire(ctx, r.Key, r.TTL)
		}
		return len(value), nil
	case r.Kind == intentKindHash && r.Intent == intentDelete:
		return 0, store.HDelete(ctx, r.Key, r.Field)
	default:
		return 0, fmt.Errorf("rdb journal record is invalid: %s %s", r.Kind, r.Intent)
	}
}

func (r journalRecord) applyReplay(ctx context.Context, store store) error {
	switch {
	case r.Kind == intentKindSet && r.Intent == intentSave:
		value, err := r.value()
		if err != nil {
			return err
		}
		return store.Set(ctx, r.Key, value, r.TTL)
	case r.Kind == intentKindSet && r.Intent == intentDelete:
		return store.Delete(ctx, r.Key)
	case r.Kind == intentKindHash && r.Intent == intentSave:
		value, err := r.value()
		if err != nil {
			return err
		}
		return store.HSet(ctx, r.Key, r.Field, value)
	case r.Kind == intentKindHash && r.Intent == intentDelete:
		return store.HDelete(ctx, r.Key, r.Field)
	default:
		return fmt.Errorf("rdb journal record is invalid: %s %s", r.Kind, r.Intent)
	}
}

func (r journalRecord) value() ([]byte, error) {
	return base64.StdEncoding.DecodeString(r.ValueBase64)
}
