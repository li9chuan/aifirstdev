package rdb

import (
	"context"
	"encoding/base64"
	"errors"
	"time"
)

const (
	intentKindSet  = "set"
	intentKindHash = "hash"

	intentSave   = "save"
	intentDelete = "delete"
)

type savePayload interface {
	MarshalQueued() ([]byte, bool, error)
}

// entityKey preserves field boundaries; save and delete share the same identity.
type entityKey struct {
	kind  string
	key   string
	field string
}

type finalIntent struct {
	kind    string
	intent  string
	key     string
	field   string
	ttl     time.Duration
	payload savePayload
}

func setSaveIntent(key string, ttl time.Duration, payload savePayload) finalIntent {
	return finalIntent{kind: intentKindSet, intent: intentSave, key: key, ttl: ttl, payload: payload}
}

func setDeleteIntent(key string) finalIntent {
	return finalIntent{kind: intentKindSet, intent: intentDelete, key: key}
}

func hashSaveIntent(key, field string, ttl time.Duration, payload savePayload) finalIntent {
	return finalIntent{kind: intentKindHash, intent: intentSave, key: key, field: field, ttl: ttl, payload: payload}
}

func hashDeleteIntent(key, field string) finalIntent {
	return finalIntent{kind: intentKindHash, intent: intentDelete, key: key, field: field}
}

func (op finalIntent) entityKey() entityKey {
	return entityKey{kind: op.kind, key: op.key, field: op.field}
}

func (op finalIntent) apply(ctx context.Context, store store) error {
	_, err := op.applyMeasured(ctx, store)
	return err
}

func (op finalIntent) applyMeasured(ctx context.Context, store store) (int, error) {
	record, ok, err := op.materialize("", 0, nil)
	if errors.Is(err, ErrJournalPayloadMissing) {
		return 0, nil
	}
	if err != nil || !ok {
		return 0, err
	}
	return record.applyLiveMeasured(ctx, store)
}

func (op finalIntent) materialize(source string, attempts int, cause error) (journalRecord, bool, error) {
	record := journalRecord{
		Source:   source,
		Kind:     op.kind,
		Intent:   op.intent,
		Key:      op.key,
		Field:    op.field,
		TTL:      op.ttl,
		Attempts: attempts,
		Ts:       time.Now().UnixMilli(),
	}
	if cause != nil {
		record.Err = cause.Error()
	}
	if op.intent == intentDelete {
		return record, true, nil
	}
	raw, ok, err := op.payload.MarshalQueued()
	if err != nil {
		return journalRecord{}, false, err
	}
	if !ok {
		return journalRecord{}, false, ErrJournalPayloadMissing
	}
	record.ValueBase64 = base64.StdEncoding.EncodeToString(raw)
	return record, true, nil
}
