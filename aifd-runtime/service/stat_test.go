package service

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	"github.com/li9chuan/aifirstdev/aifd-log"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

type serviceStatEvent struct {
	Count int `stat:"count"`
}

type serviceStatModule struct{ routes int }

func (*serviceStatModule) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{ID: "test.stat"}
}
func (*serviceStatModule) Recover(context.Context) error {
	type header struct {
		Origin string `stat:"origin"`
	}
	return logging.RecordStat(serviceStatEvent{Count: 7}, func(string) any {
		return header{Origin: "worker"}
	})
}
func (*serviceStatModule) Start(context.Context) error       { return nil }
func (*serviceStatModule) Close(context.Context) error       { return nil }
func (m *serviceStatModule) RegisterRPC(*rpc.Registry) error { m.routes++; return nil }

func TestServiceCanRecordNamedEventDuringRecoveryBeforeRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat.log")
	previous := logging.Default()
	closeLog, err := logging.InitDefault(logging.Config{
		LocalPath: filepath.Join(dir, "run.log"), Stat: &logging.StatConfig{LocalPath: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeLog(); err != nil {
			t.Error(err)
		}
		logging.SetDefault(previous)
	})
	module := &serviceStatModule{}
	svc := NewService(nil, module)
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.routes != 0 {
		t.Fatal("recovery unexpectedly registered routes")
	}
	if err := svc.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(rows) != 1 || strings.Join(rows[0], ",") != "worker,7" {
		t.Fatalf("rows=%v error=%v", rows, err)
	}
	if module.routes != 1 || logging.GetStatCounters().LocalWritten != 1 {
		t.Fatal("stat discovery affected route registration or duplicated the event")
	}
}
