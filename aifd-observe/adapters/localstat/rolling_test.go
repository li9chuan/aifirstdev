package localstat

import (
	"testing"
	"time"
)

func TestRollingCounterExpiresWindow(t *testing.T) {
	now := time.Unix(100, 0)
	counter := &RollingCounter{window: newRollingWindow(10*time.Second, 5, func() time.Time {
		return now
	})}
	counter.Add(2)
	counter.Add(3)
	if got := counter.Value(); got != 5 {
		t.Fatalf("Value = %v, want 5", got)
	}
	now = now.Add(11 * time.Second)
	if got := counter.Value(); got != 0 {
		t.Fatalf("Value after window = %v, want 0", got)
	}
}

func TestRollingSummaryReturnsSumAndCount(t *testing.T) {
	now := time.Unix(200, 0)
	summary := &RollingSummary{window: newRollingWindow(10*time.Second, 5, func() time.Time {
		return now
	})}
	summary.Add(4)
	summary.Add(6)
	sum, count := summary.Value()
	if sum != 10 || count != 2 {
		t.Fatalf("Value = %v/%d, want 10/2", sum, count)
	}
	now = now.Add(3 * time.Second)
	summary.Add(5)
	sum, count = summary.Value()
	if sum != 15 || count != 3 {
		t.Fatalf("Value after advance = %v/%d, want 15/3", sum, count)
	}
}

func TestRollingSummaryReset(t *testing.T) {
	summary := NewRollingSummary(time.Minute, 6)
	summary.Add(1)
	summary.Reset()
	sum, count := summary.Value()
	if sum != 0 || count != 0 {
		t.Fatalf("Value after reset = %v/%d, want 0/0", sum, count)
	}
}
