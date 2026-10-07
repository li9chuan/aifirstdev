package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestSinkWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.jsonl")
	sink, err := New(path)
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if err := sink.Write(context.Background(), []contracts.Event{
		contracts.Stat("event-1", "friend.add", "uid:1", 1, map[string]string{"sid": "1"}),
	}); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open error = %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("missing jsonl line")
	}
	var event contracts.Event
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if event.Kind != contracts.KindStat || event.Name != "friend.add" || event.Dimensions["sid"] != "1" {
		t.Fatalf("event = %+v", event)
	}
}
