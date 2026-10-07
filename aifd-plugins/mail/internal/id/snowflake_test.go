package id

import (
	"sync"
	"testing"
	"time"
)

func TestSnowflakeMonotonic(t *testing.T) {
	var now int64 = defaultEpochMilli + 1
	gen, err := NewSnowflake(1, WithNowMillis(func() int64 {
		return now
	}))
	if err != nil {
		t.Fatalf("NewSnowflake error = %v", err)
	}
	first, err := gen.NextID()
	if err != nil {
		t.Fatalf("first NextID error = %v", err)
	}
	now++
	second, err := gen.NextID()
	if err != nil {
		t.Fatalf("second NextID error = %v", err)
	}
	if second <= first {
		t.Fatalf("ids not monotonic: first=%d second=%d", first, second)
	}
}

func TestSnowflakeConcurrentUnique(t *testing.T) {
	gen, err := NewSnowflake(2)
	if err != nil {
		t.Fatalf("NewSnowflake error = %v", err)
	}
	const count = 512
	ids := make(chan int64, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := gen.NextID()
			if err != nil {
				t.Errorf("NextID error = %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]struct{}, count)
	for id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("ids len = %d, want %d", len(seen), count)
	}
}

func TestSnowflakeRejectsInvalidNode(t *testing.T) {
	if _, err := NewSnowflake(maxNode + 1); err == nil {
		t.Fatal("NewSnowflake error = nil, want invalid node")
	}
}

func TestSnowflakeRejectsClockBackwards(t *testing.T) {
	values := []int64{time.Now().UnixMilli(), time.Now().UnixMilli() - 1}
	gen, err := NewSnowflake(1, WithNowMillis(func() int64 {
		next := values[0]
		values = values[1:]
		return next
	}))
	if err != nil {
		t.Fatalf("NewSnowflake error = %v", err)
	}
	if _, err := gen.NextID(); err != nil {
		t.Fatalf("first NextID error = %v", err)
	}
	if _, err := gen.NextID(); err == nil {
		t.Fatal("second NextID error = nil, want clock backwards")
	}
}
