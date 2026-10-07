package runtime

import (
	"testing"
	"time"
)

func TestStatsCollectorSnapshotAggregates(t *testing.T) {
	nowSec := int64(100)
	collector := NewStatsCollector("world.g1", time.Minute)
	collector.now = func() time.Time {
		return time.Unix(nowSec, 0)
	}

	collector.RecordReqOut("RankUpdate", 10)
	collector.RecordReqOut("RPC.RankUpdate", 30)
	collector.RecordReqOut("RankUpdate", 20)
	collector.RecordReqIn("RankUpdate", 8)
	collector.RecordRspOut("RankUpdate", 12)
	collector.RecordRspIn("RankUpdate", 16)
	collector.RecordHandle("RankUpdate", 2*time.Millisecond)
	collector.RecordHandle("RankUpdate", 5*time.Millisecond)

	snapshot := collector.Snapshot()
	if snapshot.Service != "world.g1" {
		t.Fatalf("Service = %s, want world.g1", snapshot.Service)
	}
	if snapshot.WindowSec != 60 {
		t.Fatalf("WindowSec = %d, want 60", snapshot.WindowSec)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Name != "RankUpdate" {
		t.Fatalf("unexpected stats %+v", snapshot.Stats)
	}
	stat := snapshot.Stats[0]
	if stat.Total.ReqOut.Count != 3 || stat.Total.ReqOut.TotalBytes != 60 {
		t.Fatalf("unexpected reqOut total %+v", stat.Total.ReqOut)
	}
	if stat.Total.ReqOut.MinBytes != 10 || stat.Total.ReqOut.MaxBytes != 30 || stat.Total.ReqOut.AvgBytes != 20 {
		t.Fatalf("unexpected reqOut aggregate %+v", stat.Total.ReqOut)
	}
	if stat.Total.Handle.Count != 2 || stat.Total.Handle.TotalUs != 7000 || stat.Total.Handle.AvgUs != 3500 {
		t.Fatalf("unexpected handle total %+v", stat.Total.Handle)
	}
	if stat.Recent.ReqIn.Count != 1 || stat.Recent.RspOut.Count != 1 || stat.Recent.RspIn.Count != 1 {
		t.Fatalf("unexpected recent %+v", stat.Recent)
	}
}

func TestStatsCollectorSnapshotWindowExpires(t *testing.T) {
	nowSec := int64(10)
	collector := NewStatsCollector("world.g1", time.Minute)
	collector.now = func() time.Time {
		return time.Unix(nowSec, 0)
	}

	collector.RecordReqOut("RankList", 10)
	nowSec = 71
	collector.RecordReqOut("RankList", 20)
	collector.RecordHandle("RankList", time.Millisecond)

	stat := collector.Snapshot().Stats[0]
	if stat.Total.ReqOut.Count != 2 || stat.Total.ReqOut.TotalBytes != 30 {
		t.Fatalf("unexpected total %+v", stat.Total.ReqOut)
	}
	if stat.Recent.ReqOut.Count != 1 || stat.Recent.ReqOut.TotalBytes != 20 {
		t.Fatalf("unexpected recent %+v", stat.Recent.ReqOut)
	}
}
