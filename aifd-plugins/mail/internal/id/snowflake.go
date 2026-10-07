// Package id contains mail-plugin-local ID generation implementations.
package id

import (
	"fmt"
	"sync"
	"time"
)

const (
	sequenceBits = 12
	nodeBits     = 10

	maxSequence = int64(1<<sequenceBits) - 1
	maxNode     = int64(1<<nodeBits) - 1
	nodeShift   = sequenceBits
	timeShift   = sequenceBits + nodeBits

	defaultEpochMilli = int64(1704067200000) // 2024-01-01T00:00:00Z
)

// Snowflake generates positive, time-ordered IDs for mail records.
type Snowflake struct {
	mu        sync.Mutex
	nodeID    int64
	epochMs   int64
	lastMs    int64
	sequence  int64
	nowMillis func() int64
}

// Option configures a Snowflake generator.
type Option func(*Snowflake)

// WithEpoch sets the custom epoch used in the high timestamp bits.
func WithEpoch(t time.Time) Option {
	return func(g *Snowflake) {
		g.epochMs = t.UTC().UnixMilli()
	}
}

// WithNowMillis overrides the millisecond clock for tests.
func WithNowMillis(now func() int64) Option {
	return func(g *Snowflake) {
		if now != nil {
			g.nowMillis = now
		}
	}
}

// NewSnowflake creates a mail ID generator with a 10-bit node ID and 12-bit per-millisecond sequence.
func NewSnowflake(nodeID int64, opts ...Option) (*Snowflake, error) {
	if nodeID < 0 || nodeID > maxNode {
		return nil, fmt.Errorf("mail snowflake node id %d out of range [0,%d]", nodeID, maxNode)
	}
	g := &Snowflake{
		nodeID:    nodeID,
		epochMs:   defaultEpochMilli,
		nowMillis: func() int64 { return time.Now().UnixMilli() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g, nil
}

// NextID returns the next positive, time-ordered mail ID.
func (g *Snowflake) NextID() (int64, error) {
	if g == nil {
		return 0, fmt.Errorf("mail snowflake generator is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.nowMillis()
	if now < g.lastMs {
		return 0, fmt.Errorf("mail snowflake clock moved backwards: now=%d last=%d", now, g.lastMs)
	}
	if now == g.lastMs {
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			for now <= g.lastMs {
				now = g.nowMillis()
			}
		}
	} else {
		g.sequence = 0
	}
	g.lastMs = now

	elapsed := now - g.epochMs
	if elapsed < 0 {
		return 0, fmt.Errorf("mail snowflake time before epoch: now=%d epoch=%d", now, g.epochMs)
	}
	return (elapsed << timeShift) | (g.nodeID << nodeShift) | g.sequence, nil
}
