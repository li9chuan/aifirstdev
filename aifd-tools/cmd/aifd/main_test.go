package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if record := os.Getenv("AIFD_FAKE_TOOL_RECORD"); record != "" {
		lines := append([]string{"PATH=" + os.Getenv("PATH")}, os.Args[1:]...)
		_ = os.WriteFile(record, []byte(strings.Join(lines, "\n")), 0644)
		os.Exit(0)
	}
	if os.Getenv("AIFD_FAKE_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
	}
	os.Exit(m.Run())
}

func TestUpdateUsesWholeFramework(t *testing.T) {
	for _, args := range [][]string{{"update"}, {"update", "--check"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr, t.TempDir()); code != 0 {
			t.Fatalf("run(%v) = %d: %s", args, code, stderr.String())
		}
		for _, want := range []string{"no remote check or update was performed", "go get github.com/li9chuan/aifirstdev@<version>"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("run(%v) missing %q: %s", args, want, stdout.String())
			}
		}
	}
	for _, args := range [][]string{{"update", "rdb"}, {"update", "--check", "rdb"}, {"update", "unknown"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr, t.TempDir()); code != 2 || stdout.Len() != 0 {
			t.Fatalf("run(%v) = %d, stdout=%q, stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestToolsPathWithExplicitExecutables(t *testing.T) {
	dir := t.TempDir()
	exe := os.Args[0]
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"tools", "path",
		"--project", dir,
		"--go", exe,
		"--protoc", exe,
		"--protoc-gen-go", exe,
		"--luban", exe,
		"--redis-server", exe,
		"--wails", exe,
		"--makensis", exe,
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, name := range []string{"go", "protoc", "protoc-gen-go", "luban", "redis-server", "wails", "nsis"} {
		if !strings.Contains(out, name) {
			t.Fatalf("tools path output missing %s: %s", name, out)
		}
	}
}

func TestToolsPathIgnoresMissingOptionalTools(t *testing.T) {
	dir := t.TempDir()
	exe := os.Args[0]
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"tools", "path",
		"--project", dir,
		"--go", exe,
		"--protoc", exe,
		"--protoc-gen-go", exe,
		"--luban", exe,
		"--redis-server", exe,
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "optional-missing") {
		t.Fatalf("tools path should show optional missing tools: %s", stdout.String())
	}
}

func TestProtoGenExecutesProtoc(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args.txt")
	t.Setenv("AIFD_FAKE_TOOL_RECORD", record)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"proto", "gen",
		"--project", dir,
		"--protoc", os.Args[0],
		"--protoc-gen-go", os.Args[0],
		"--proto_path", "proto",
		"--go_out", "gen/go",
		"--out", "gen/schema.pb",
		"--extra", "--experimental_allow_proto3_optional",
		"--include", "proto/aifd/proxy/proxy.proto",
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	got := readRecord(t, record)
	for _, want := range []string{
		"--proto_path=proto",
		"--go_out=gen/go",
		"--descriptor_set_out=gen/schema.pb",
		"--experimental_allow_proto3_optional",
		"proto/aifd/proxy/proxy.proto",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("protoc args missing %q: %s", want, got)
		}
	}
}

func TestProtoGenInjectsProjectPluginPath(t *testing.T) {
	dir := t.TempDir()
	pluginBin := filepath.Join(dir, ".aifd", "tools", runtime.GOOS+"-"+runtime.GOARCH, "protoc-gen-go", "v1.0.0", "bin")
	if err := os.MkdirAll(pluginBin, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginBin, exeName("protoc-gen-go")), []byte("fake"), 0755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aifd.lock"), []byte("tools.protoc-gen-go: v1.0.0\n"), 0644); err != nil {
		t.Fatalf("WriteFile lock error = %v", err)
	}
	record := filepath.Join(dir, "args.txt")
	t.Setenv("AIFD_FAKE_TOOL_RECORD", record)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"proto", "gen",
		"--project", dir,
		"--protoc", os.Args[0],
		"--go_out", "gen/go",
		"--include", "example.proto",
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	got := readRecord(t, record)
	if !strings.Contains(got, pluginBin) {
		t.Fatalf("PATH did not include plugin bin %q: %s", pluginBin, got)
	}
}

func TestConfigGenExecutesLuban(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args.txt")
	t.Setenv("AIFD_FAKE_TOOL_RECORD", record)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"config", "gen",
		"--project", dir,
		"--luban", os.Args[0],
		"--conf", "luban.conf",
		"--out", "gen/cfg",
		"--target", "go",
		"--extra=--validationFailAsError",
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	got := readRecord(t, record)
	for _, want := range []string{
		"--conf=luban.conf",
		"--out=gen/cfg",
		"--target=go",
		"--validationFailAsError",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Luban args missing %q: %s", want, got)
		}
	}
}

func TestDevStatusStopped(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"dev", "status", "--project", dir, "--service", "redis"}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "redis stopped") || !strings.Contains(out, ".aifd") {
		t.Fatalf("dev status output = %s", out)
	}
}

func TestToolsInstallWithFakeManifest(t *testing.T) {
	dir := t.TempDir()
	archive := testZip(t, map[string]string{
		"bin/" + exeName("protoc"): "fake",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := `{"tools":{"protoc":{"version":"1.0.0","type":"archive","url":"` + server.URL + `","archive":"zip","bin":["bin/` + exeName("protoc") + `"]}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	t.Setenv("AIFD_FAKE_GO_INSTALL", "1")
	var stdout, stderr bytes.Buffer
	code := run([]string{"tools", "install", "--project", dir, "--manifest", manifestPath, "--tool", "protoc"}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	installed := filepath.Join(dir, ".aifd", "tools", runtime.GOOS+"-"+runtime.GOARCH, "protoc", "1.0.0", "bin", exeName("protoc"))
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("installed protoc missing: %v", err)
	}
	lockData, err := os.ReadFile(filepath.Join(dir, "aifd.lock"))
	if err != nil {
		t.Fatalf("ReadFile lock error = %v", err)
	}
	if !strings.Contains(string(lockData), "tools.protoc: 1.0.0") {
		t.Fatalf("lock not updated: %s", string(lockData))
	}
}

func TestToolsInstallDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := `{"tools":{"protoc":{"version":"1.0.0","type":"archive","url":"http://example.invalid/protoc.zip","archive":"zip","bin":["protoc"]}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"tools", "install", "--project", dir, "--manifest", manifestPath, "--tool", "protoc", "--dry-run"}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".aifd")); !os.IsNotExist(err) {
		t.Fatalf(".aifd should not exist: %v", err)
	}
}

func TestToolsInstallWailsDryRunWithFakeManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := `{"tools":{"wails":{"version":"v1.0.0","type":"go-install","module":"example.com/wails","bin":["` + exeName("wails") + `"]}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"tools", "install",
		"--project", dir,
		"--manifest", manifestPath,
		"--tool", "wails",
		"--dry-run",
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "wails") || !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".aifd")); !os.IsNotExist(err) {
		t.Fatalf(".aifd should not exist: %v", err)
	}
}

func TestToolsInstallNSISDryRunWithFakeManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := `{"tools":{"nsis":{"version":"3.10","type":"archive","url":"http://example.invalid/nsis.zip","archive":"zip","bin":["` + exeName("makensis") + `"]}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"tools", "install",
		"--project", dir,
		"--manifest", manifestPath,
		"--tool", "nsis",
		"--dry-run",
	}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "nsis") || !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".aifd")); !os.IsNotExist(err) {
		t.Fatalf(".aifd should not exist: %v", err)
	}
}

func TestDevUpDownWithFakeService(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIFD_FAKE_SLEEP", "1")
	var upOut, upErr bytes.Buffer
	code := run([]string{
		"dev", "up",
		"--project", dir,
		"--service", "redis",
		"--redis-server", os.Args[0],
	}, &upOut, &upErr, dir)
	if code != 0 {
		t.Fatalf("dev up code = %d, stderr = %s", code, upErr.String())
	}
	if !strings.Contains(upOut.String(), "redis started") {
		t.Fatalf("dev up output = %s", upOut.String())
	}

	var statusOut, statusErr bytes.Buffer
	code = run([]string{"dev", "status", "--project", dir, "--service", "redis"}, &statusOut, &statusErr, dir)
	if code != 0 {
		t.Fatalf("dev status code = %d, stderr = %s", code, statusErr.String())
	}
	if !strings.Contains(statusOut.String(), "redis running") {
		t.Fatalf("dev status output = %s", statusOut.String())
	}

	var downOut, downErr bytes.Buffer
	code = run([]string{"dev", "down", "--project", dir, "--service", "redis"}, &downOut, &downErr, dir)
	if code != 0 {
		t.Fatalf("dev down code = %d, stderr = %s", code, downErr.String())
	}
	if !strings.Contains(downOut.String(), "redis stopped") {
		t.Fatalf("dev down output = %s", downOut.String())
	}
}

func TestDevAllDefaultsToRedisOnly(t *testing.T) {
	got := selectedServices("all")
	if strings.Join(got, ",") != "redis" {
		t.Fatalf("selectedServices(all) = %v, want redis only", got)
	}
}

func TestLoadArgsFileFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".aifd"), 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".aifd", "proto.args"), []byte("--proto_path=proto\n# comment\nexample.proto\n"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	record := filepath.Join(dir, "args.txt")
	t.Setenv("AIFD_FAKE_TOOL_RECORD", record)
	var stdout, stderr bytes.Buffer
	code := run([]string{"proto", "gen", "--project", dir, "--protoc", os.Args[0]}, &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("run code = %d, stderr = %s", code, stderr.String())
	}
	got := readRecord(t, record)
	if !strings.Contains(got, "--proto_path=proto") || !strings.Contains(got, "example.proto") {
		t.Fatalf("fallback args = %s", got)
	}
}

func TestWindowsExecutableFlagsUseCurrentTestBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific smoke check")
	}
	if !strings.HasSuffix(strings.ToLower(os.Args[0]), ".exe") {
		t.Fatalf("test binary should be an exe on Windows: %s", os.Args[0])
	}
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("Write(%s) error = %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close error = %v", err)
	}
	return buf.Bytes()
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
