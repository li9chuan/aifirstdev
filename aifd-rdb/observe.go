package rdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

const (
	metricQueueWait    = "rdb.queue.wait"
	metricSaveDuration = "rdb.save.duration_ms"
	metricSaveError    = "rdb.save.error"
	metricSaveRetry    = "rdb.save.retry"
	metricCloseJournal = "rdb.close.journal"
	metricPayloadLarge = "rdb.payload.large"
)

func (db *DB) applyFinalIntent(ctx context.Context, stage string, op finalIntent, attempts int) error {
	start := time.Now()
	payloadBytes, err := op.applyMeasured(ctx, db.store)
	db.reportSaveDuration(ctx, stage, op, attempts, time.Since(start))
	db.reportLargePayload(ctx, op, payloadBytes)
	return err
}

func (db *DB) reportQueueDepth(ctx context.Context, size int) {
	db.writeMetric(ctx, metricQueueWait, float64(size), nil, nil)
}

func (db *DB) reportSaveDuration(ctx context.Context, stage string, op finalIntent, attempts int, duration time.Duration) {
	db.writeMetric(ctx, metricSaveDuration, float64(duration.Milliseconds()), opDimensions(stage, op, attempts), nil)
}

func (db *DB) reportSaveError(ctx context.Context, stage string, op finalIntent, attempts int, retryable, final bool) {
	dimensions := opDimensions(stage, op, attempts)
	dimensions["retryable"] = strconv.FormatBool(retryable)
	dimensions["final"] = strconv.FormatBool(final)
	db.writeMetric(ctx, metricSaveError, 1, dimensions, db.opFields(op, 0))
	if retryable {
		db.writeMetric(ctx, metricSaveRetry, float64(attempts), dimensions, db.opFields(op, 0))
	}
}

func (db *DB) reportCloseJournal(ctx context.Context, count int) {
	db.writeMetric(ctx, metricCloseJournal, float64(count), nil, nil)
}

func (db *DB) reportLargePayload(ctx context.Context, op finalIntent, payloadBytes int) {
	if payloadBytes <= 0 || op.intent != intentSave {
		return
	}
	threshold := db.largeSetPayloadBytes
	if op.kind == intentKindHash {
		threshold = db.largeHashPayloadBytes
	}
	if threshold <= 0 || payloadBytes <= threshold {
		return
	}
	db.writeMetric(ctx, metricPayloadLarge, float64(payloadBytes), opDimensions("", op, 0), db.opFields(op, payloadBytes))
}

func (db *DB) writeMetric(ctx context.Context, name string, value float64, dimensions map[string]string, fields map[string]any) {
	if db == nil || db.observeSink == nil {
		return
	}
	event := observe.Metric(name, value, dimensions)
	event.Fields = fields
	_ = db.observeSink.Write(ctx, []observe.Event{event})
}

func opDimensions(stage string, op finalIntent, attempts int) map[string]string {
	dimensions := map[string]string{
		"kind":   op.kind,
		"intent": op.intent,
	}
	if stage != "" {
		dimensions["stage"] = stage
	}
	if attempts > 0 {
		dimensions["attempts"] = strconv.Itoa(attempts)
	}
	return dimensions
}

func (db *DB) opFields(op finalIntent, payloadBytes int) map[string]any {
	fields := make(map[string]any, 3)
	if db != nil && db.observeRawKeys {
		fields["key"] = op.key
		if op.field != "" {
			fields["field"] = op.field
		}
	} else {
		fields["key_hash"] = observeHash(op.key)
		if op.field != "" {
			fields["field_hash"] = observeHash(op.field)
		}
	}
	if payloadBytes > 0 {
		fields["payload_bytes"] = payloadBytes
	}
	return fields
}

func observeHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
