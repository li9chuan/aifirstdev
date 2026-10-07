package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/lockfile"
)

func TestResolveUsesEnvBeforePath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "protoc")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(exe, []byte("fake"), 0755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	t.Setenv("AIFD_PROTOC", exe)

	resolver := Resolver{
		ProjectDir: dir,
		LookPath: func(string) (string, error) {
			return filepath.Join(dir, "path-protoc"), nil
		},
	}
	result := resolver.Resolve(context.Background(), Spec{Name: ToolProtoc, Env: "AIFD_PROTOC", ExeNames: exeNames("protoc")}, false)
	if result.Path != exe || result.Source != "env:AIFD_PROTOC" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolveProjectToolVersionPath(t *testing.T) {
	dir := t.TempDir()
	lock := lockfile.Lock{Tools: map[string]string{ToolProtoc: "3.20.3"}}
	exeName := exeNames("protoc")[0]
	exe := filepath.Join(ProjectToolsDir(dir), runtime.GOOS+"-"+runtime.GOARCH, ToolProtoc, "3.20.3", "bin", exeName)
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(exe, []byte("fake"), 0755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result := Resolver{ProjectDir: dir, Lock: lock}.Resolve(context.Background(), Spec{Name: ToolProtoc, Env: "AIFD_PROTOC", ExeNames: exeNames("protoc")}, false)
	if result.Path != exe || result.Source != "project" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolveLegacyProjectToolVersionPath(t *testing.T) {
	dir := t.TempDir()
	lock := lockfile.Lock{Tools: map[string]string{ToolProtoc: "3.20.3"}}
	exeName := exeNames("protoc")[0]
	exe := filepath.Join(ProjectToolsDir(dir), runtime.GOOS+"-"+runtime.GOARCH, ToolProtoc, "3.20.3", exeName)
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(exe, []byte("fake"), 0755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result := Resolver{ProjectDir: dir, Lock: lock}.Resolve(context.Background(), Spec{Name: ToolProtoc, Env: "AIFD_PROTOC", ExeNames: exeNames("protoc")}, false)
	if result.Path != exe || result.Source != "project" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolveMissing(t *testing.T) {
	result := Resolver{
		ProjectDir: t.TempDir(),
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
	}.Resolve(context.Background(), Spec{Name: ToolRedis, Env: "AIFD_REDIS_SERVER", ExeNames: exeNames("redis-server")}, false)
	if !result.Missing || result.Error == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolveUnsupportedWhenNoExecutableExists(t *testing.T) {
	resolver := Resolver{
		ProjectDir: t.TempDir(),
		GOOS:       "windows",
		GOARCH:     "amd64",
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
	}
	spec, _ := SpecByName(ToolRedis)
	result := resolver.Resolve(context.Background(), spec, false)
	if !result.Unsupported || result.Missing || result.Error == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSpecByNameWails(t *testing.T) {
	spec, ok := SpecByName(ToolWails)
	if !ok {
		t.Fatal("SpecByName(wails) ok = false")
	}
	if spec.Env != "AIFD_WAILS" || len(spec.VersionArgs) != 1 || spec.VersionArgs[0] != "version" || !spec.Optional {
		t.Fatalf("wails spec = %+v", spec)
	}
	if len(spec.ExeNames) == 0 {
		t.Fatalf("wails exe names empty: %+v", spec)
	}
}

func TestResolveWailsUsesV3ProjectBinaryAndIgnoresV2Path(t *testing.T) {
	t.Setenv("AIFD_WAILS", "")
	dir := t.TempDir()
	spec, _ := SpecByName(ToolWails)
	resolver := Resolver{
		ProjectDir: dir,
		Lock:       lockfile.Lock{Tools: map[string]string{ToolWails: "v3.0.0-beta.28"}},
		LookPath: func(name string) (string, error) {
			if name == "wails" || name == "wails.exe" {
				t.Fatal("Wails 2 executable must not be searched")
			}
			return "", os.ErrNotExist
		},
	}
	if result := resolver.Resolve(context.Background(), spec, false); !result.Missing {
		t.Fatalf("missing Wails 3 result = %+v", result)
	}
	exe := filepath.Join(ProjectToolsDir(dir), runtime.GOOS+"-"+runtime.GOARCH, ToolWails, "v3.0.0-beta.28", "bin", exeNames("wails3")[0])
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if result := resolver.Resolve(context.Background(), spec, false); result.Path != exe || result.Source != "project" {
		t.Fatalf("project Wails 3 result = %+v", result)
	}
}

func TestSpecByNameNSIS(t *testing.T) {
	spec, ok := SpecByName(ToolNSIS)
	if !ok {
		t.Fatal("SpecByName(nsis) ok = false")
	}
	if spec.Env != "AIFD_MAKENSIS" || len(spec.VersionArgs) != 1 || spec.VersionArgs[0] != "/VERSION" || !spec.Optional {
		t.Fatalf("nsis spec = %+v", spec)
	}
	if len(spec.ExeNames) == 0 {
		t.Fatalf("nsis exe names empty: %+v", spec)
	}
}

func TestAnyMissingIgnoresOptionalTools(t *testing.T) {
	err := AnyMissing([]Result{
		{Name: ToolNSIS, Missing: true, Optional: true},
	})
	if err != nil {
		t.Fatalf("AnyMissing optional err = %v", err)
	}
	err = AnyMissing([]Result{
		{Name: ToolProtoc, Missing: true},
	})
	if err == nil {
		t.Fatal("AnyMissing required tool err = nil")
	}
}

func TestVersionMatchesAllowsPlaceholders(t *testing.T) {
	cases := []struct {
		expected string
		detected string
		want     bool
	}{
		{expected: "any", detected: "tool 1.2.3", want: true},
		{expected: "1.x.x", detected: "tool 1.2.3", want: true},
		{expected: "v4.8.0", detected: "Luban 4.8.0+abcdef", want: true},
		{expected: "7.2.0", detected: "redis-server 7.0.0", want: false},
	}
	for _, tc := range cases {
		if got := versionMatches(tc.expected, tc.detected); got != tc.want {
			t.Fatalf("versionMatches(%q, %q) = %v, want %v", tc.expected, tc.detected, got, tc.want)
		}
	}
}
