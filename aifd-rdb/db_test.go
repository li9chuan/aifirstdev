package rdb

import (
	"errors"
	"testing"
	"time"
)

func TestNewMemoryDBReturnsInvalidOptionError(t *testing.T) {
	db, err := NewMemoryDB(WithSaveDelay(time.Second, time.Millisecond))
	if !errors.Is(err, ErrInvalidSaveDelay) {
		t.Fatalf("NewMemoryDB error = %v, want %v", err, ErrInvalidSaveDelay)
	}
	if db != nil {
		t.Fatalf("NewMemoryDB db = %#v, want nil", db)
	}
}

func TestNewMemoryDBRejectsInvalidSaveStayMin(t *testing.T) {
	db, err := NewMemoryDB(WithSaveStayMin(-time.Nanosecond))
	if !errors.Is(err, ErrInvalidSaveDelay) {
		t.Fatalf("NewMemoryDB error = %v, want %v", err, ErrInvalidSaveDelay)
	}
	if db != nil {
		t.Fatalf("NewMemoryDB db = %#v, want nil", db)
	}
}

func TestApplyDBOptionsDisablesSaveStayMinByDefault(t *testing.T) {
	cfg, err := applyDBOptions(nil)
	if err != nil {
		t.Fatalf("applyDBOptions error = %v", err)
	}
	if cfg.saveStayMin != 0 {
		t.Fatalf("saveStayMin = %v, want 0", cfg.saveStayMin)
	}
}

func TestTakeNextWaitsForSaveStayMin(t *testing.T) {
	db := newDB(newMemoryStore(), options{
		minSaveDelay: time.Millisecond,
		maxSaveDelay: time.Millisecond,
		saveStayMin:  time.Hour,
	})
	db.lastSave = time.Now().Add(-time.Hour)
	db.queueMu.Lock()
	db.wait.PushBack(setSaveIntent("stay", 0, nil))
	db.queueMu.Unlock()
	beforeLastSave := db.lastSave

	if op, _, ok := db.takeNext(false); ok {
		t.Fatalf("takeNext(false) = %+v ok=%v, want not ready", op, ok)
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize after not ready = %d, want 1", db.WaitSize())
	}
	if !db.lastSave.Equal(beforeLastSave) {
		t.Fatalf("lastSave changed from %v to %v", beforeLastSave, db.lastSave)
	}

	op, _, ok := db.takeNext(true)
	if !ok || op.entityKey() != (entityKey{kind: intentKindSet, key: "stay"}) {
		t.Fatalf("takeNext(true) = %+v ok=%v, want forced set:stay", op, ok)
	}
}

func TestTakeNextPopsAfterSaveStayMin(t *testing.T) {
	db := newDB(newMemoryStore(), options{
		minSaveDelay: time.Millisecond,
		maxSaveDelay: time.Millisecond,
		saveStayMin:  time.Second,
	})
	db.lastSave = time.Now().Add(-time.Hour)
	db.queueMu.Lock()
	db.wait.PushBack(setSaveIntent("ready", 0, nil))
	key := entityKey{kind: intentKindSet, key: "ready"}
	entry := db.wait.index[key].Value.(waitEntry)
	entry.firstQueued = time.Now().Add(-2 * time.Second)
	db.wait.index[key].Value = entry
	db.queueMu.Unlock()

	op, _, ok := db.takeNext(false)
	if !ok || op.entityKey() != key {
		t.Fatalf("takeNext(false) = %+v ok=%v, want ready set", op, ok)
	}
}

func TestMustNewMemoryDBPanicsOnInvalidOption(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("MustNewMemoryDB did not panic")
		}
		err, ok := recovered.(error)
		if !ok || !errors.Is(err, ErrInvalidSaveDelay) {
			t.Fatalf("panic value = %#v, want %v", recovered, ErrInvalidSaveDelay)
		}
	}()
	_ = MustNewMemoryDB(WithSaveDelay(time.Second, time.Millisecond))
}
