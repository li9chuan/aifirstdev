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
type otherServiceStatEvent struct {
	Count int `stat:"count"`
}

type serviceStatModule struct {
	events []logging.StatSchema
	routes int
}

func (*serviceStatModule) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{ID: "test.stat"}
}
func (*serviceStatModule) Recover(context.Context) error      { return nil }
func (*serviceStatModule) Start(context.Context) error        { return nil }
func (*serviceStatModule) Close(context.Context) error        { return nil }
func (m *serviceStatModule) RegisterRPC(*rpc.Registry) error  { m.routes++; return nil }
func (m *serviceStatModule) StatEvents() []logging.StatSchema { return m.events }

// A business wrapper explicitly forwards its existing child feature definitions.
type wrappedStatModule struct {
	serviceStatModule
	children []logging.StatEventProvider
}

func (m *wrappedStatModule) StatEvents() []logging.StatSchema {
	var events []logging.StatSchema
	for _, child := range m.children {
		events = append(events, child.StatEvents()...)
	}
	return events
}

func TestServiceCollectsWrappedFeatureStatEventsWithoutManualAggregation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat.log")
	closeLog, err := logging.InitDefault(logging.Config{LocalPath: filepath.Join(dir, "run.log"), Stat: &logging.StatConfig{LocalPath: path}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeLog(); err != nil {
			t.Error(err)
		}
	})
	child := &serviceStatModule{events: []logging.StatSchema{logging.DefineStat[serviceStatEvent]("completed", 1)}}
	wrapper := &wrappedStatModule{children: []logging.StatEventProvider{child}}
	svc := NewService(nil, wrapper, child)
	if err := svc.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	type header struct {
		Origin string `stat:"origin"`
	}
	if err := logging.RecordStat(serviceStatEvent{7}, func(logging.StatSchema) any { return header{"worker"} }); err != nil {
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
	if wrapper.routes != 1 || child.routes != 1 {
		t.Fatal("module routes were not registered")
	}
}

func TestServiceRejectsConflictingStatDefinitionsBeforeRoutes(t *testing.T) {
	dir := t.TempDir()
	closeLog, err := logging.InitDefault(logging.Config{LocalPath: filepath.Join(dir, "run.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	first := &serviceStatModule{events: []logging.StatSchema{logging.DefineStat[serviceStatEvent]("completed", 1)}}
	second := &serviceStatModule{events: []logging.StatSchema{logging.DefineStat[otherServiceStatEvent]("completed", 1)}}
	if err := NewService(nil, first, second).RegisterRPC(); err == nil {
		t.Fatal("conflict accepted")
	}
	if first.routes != 0 || second.routes != 0 {
		t.Fatal("routes registered before checking all stat definitions")
	}
}
