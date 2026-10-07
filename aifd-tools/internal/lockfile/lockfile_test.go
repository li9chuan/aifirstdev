package lockfile

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	lock, err := Parse(strings.NewReader("rpc: v0.1.3\nrdb: v0.1.2\ntools.protoc: 3.20.3\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if lock.Versions["rpc"] != "v0.1.3" {
		t.Fatalf("rpc version = %q", lock.Versions["rpc"])
	}
	if lock.Modules["rdb"] != "v0.1.2" {
		t.Fatalf("rdb module version = %q", lock.Modules["rdb"])
	}
	if lock.ToolVersion("protoc") != "3.20.3" {
		t.Fatalf("protoc version = %q", lock.ToolVersion("protoc"))
	}
}

func TestFormat(t *testing.T) {
	lock, err := Parse(strings.NewReader("tools.redis-server: 7.2.0\nrpc: v0.1.3\ntools.protoc: 3.20.3\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	got := lock.Format()
	want := "rpc: v0.1.3\ntools.protoc: 3.20.3\ntools.redis-server: 7.2.0\n"
	if got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}
