package user_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	"github.com/li9chuan/aifirstdev/aifd-log"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"github.com/li9chuan/aifirstdev/aifd-runtime/user"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type JobFinished struct {
	JobType    string `stat:"job_type"`
	Result     string `stat:"result"`
	DurationMS int64  `stat:"duration_ms"`
}

type exampleStatFeature struct{}

func (exampleStatFeature) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{ID: "example.jobs"}
}
func (exampleStatFeature) OnLoad(context.Context, *user.User[*wrapperspb.Int64Value]) error {
	return nil
}
func (exampleStatFeature) RegisterRPC(*rpc.Registry) error { return nil }

func ExampleUser_Stat() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "aifd-stat-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "stat.log")
	closeLog, err := logging.InitDefault(logging.Config{
		LocalPath: filepath.Join(dir, "service.log"),
		Stat:      &logging.StatConfig{LocalPath: path},
	})
	if err != nil {
		panic(err)
	}
	defer closeLog()
	db := rdb.MustNewMemoryDB()
	defer db.Close(ctx)
	store, err := user.NewStore(user.StoreOptions[*wrapperspb.Int64Value]{
		DB:  db,
		Key: func(uid int64) string { return "example:user:" + strconv.FormatInt(uid, 10) },
		New: func(uid int64) *wrapperspb.Int64Value { return wrapperspb.Int64(uid) },
	})
	if err != nil {
		panic(err)
	}
	type AppStatHeader struct {
		Time      string `stat:"time"`
		EventName string `stat:"event_name"`
		UID       int64  `stat:"uid"`
	}
	occurredAt := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	runtime := user.NewRuntime(rpc.NewRegistry(), store, user.NewManager[*wrapperspb.Int64Value](exampleStatFeature{}), user.WithStatHeader(
		func(owner *user.User[*wrapperspb.Int64Value], eventName string) AppStatHeader {
			return AppStatHeader{Time: occurredAt.Format("2006-01-02 15:04:05"), EventName: eventName, UID: owner.UID()}
		},
	))
	defer runtime.Close(ctx)
	if err := runtime.RegisterRPC(); err != nil {
		panic(err)
	}
	owner, err := runtime.CreateUser(ctx, 1001)
	if err != nil {
		panic(err)
	}
	owner.Stat(JobFinished{JobType: "import", Result: "success", DurationMS: 235})
	if err := closeLog(); err != nil {
		panic(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fmt.Print(string(raw))
	// Output: 2026-11-11 08:00:00,JobFinished,1001,import,success,235
}
