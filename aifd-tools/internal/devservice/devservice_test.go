package devservice

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("AIFD_FAKE_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStatusStoppedForMissingPID(t *testing.T) {
	dir := t.TempDir()
	status := GetStatus("nats", dir)
	if status.Running || status.PID != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestStopRemovesStalePID(t *testing.T) {
	dir := t.TempDir()
	runDir := RunDir(dir)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "redis.pid"), []byte("99999999\n"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	status, err := Stop("redis", dir)
	if err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if status.Running {
		t.Fatalf("status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(runDir, "redis.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file still exists: %v", err)
	}
}

func TestStartStatusStopFakeProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIFD_FAKE_SLEEP", "1")
	status, err := Start("fake", os.Args[0], nil, dir)
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if !status.Running || status.PID <= 0 {
		t.Fatalf("start status = %+v", status)
	}
	if got := GetStatus("fake", dir); !got.Running || strconv.Itoa(got.PID) == "" {
		t.Fatalf("status = %+v", got)
	}
	stopped, err := Stop("fake", dir)
	if err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if stopped.Running {
		t.Fatalf("stopped = %+v", stopped)
	}
}
