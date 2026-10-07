package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-tools/internal/lockfile"
	"github.com/li9chuan/aifirstdev/aifd-tools/internal/toolchain"
)

func TestInstallArchiveZipUpdatesLockAndSkipsExisting(t *testing.T) {
	archive := zipBytes(t, map[string]string{
		"bin/" + exeName("protoc"):            "fake",
		"include/google/protobuf/empty.proto": "syntax = \"proto3\";",
	})
	server := serveBytes(archive)
	defer server.Close()

	projectDir := t.TempDir()
	lock, err := lockfile.Parse(strings.NewReader("tools.protoc: 1.x.x\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	manifest := Manifest{Tools: map[string]Tool{
		toolchain.ToolProtoc: {
			Version: "1.0.0",
			Type:    TypeArchive,
			URL:     server.URL,
			Archive: "zip",
			SHA256:  sha256Hex(archive),
			Bin:     []string{"bin/" + exeName("protoc")},
			Include: []string{"include"},
		},
	}}

	next, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Lock:       lock,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolProtoc},
	})
	if len(results) != 1 || results[0].Status != StatusInstalled || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if got := next.ToolVersion(toolchain.ToolProtoc); got != "1.0.0" {
		t.Fatalf("lock protoc version = %q", got)
	}
	bin := filepath.Join(toolchain.ProjectToolBinDir(projectDir, runtime.GOOS, runtime.GOARCH, toolchain.ToolProtoc, "1.0.0"), exeName("protoc"))
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("installed bin missing: %v", err)
	}
	include := filepath.Join(filepath.Dir(filepath.Dir(bin)), "include", "google", "protobuf", "empty.proto")
	if _, err := os.Stat(include); err != nil {
		t.Fatalf("installed include missing: %v", err)
	}

	_, skipped := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Lock:       next,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolProtoc},
	})
	if skipped[0].Status != StatusSkipped {
		t.Fatalf("second install = %+v", skipped)
	}
}

func TestInstallArchiveTarGz(t *testing.T) {
	archive := tarGzBytes(t, map[string]string{
		"protoc-v1/" + exeName("protoc"): "fake",
	})
	server := serveBytes(archive)
	defer server.Close()

	projectDir := t.TempDir()
	manifest := Manifest{Tools: map[string]Tool{
		toolchain.ToolProtoc: {
			Version: "v1.0.0",
			Type:    TypeArchive,
			URL:     server.URL,
			Archive: "tar.gz",
			Bin:     []string{exeName("protoc")},
		},
	}}
	_, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolProtoc},
	})
	if len(results) != 1 || results[0].Status != StatusInstalled || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
}

func TestInstallArchiveCopiesBinarySiblingFiles(t *testing.T) {
	archive := zipBytes(t, map[string]string{
		"Luban/Luban.exe":                   "fake exe",
		"Luban/Luban.dll":                   "fake dll",
		"Luban/Templates/go-json/table.tpl": "fake template",
	})
	server := serveBytes(archive)
	defer server.Close()

	projectDir := t.TempDir()
	manifest := Manifest{Tools: map[string]Tool{
		toolchain.ToolLuban: {
			Version:    "v1.0.0",
			Type:       TypeArchive,
			URL:        server.URL,
			Archive:    "zip",
			Bin:        []string{"Luban.exe"},
			BinInclude: []string{"Templates"},
		},
	}}
	_, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolLuban},
		GOOS:       "windows",
		GOARCH:     "amd64",
	})
	if len(results) != 1 || results[0].Status != StatusInstalled || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	dll := filepath.Join(toolchain.ProjectToolBinDir(projectDir, "windows", "amd64", toolchain.ToolLuban, "v1.0.0"), "Luban.dll")
	if _, err := os.Stat(dll); err != nil {
		t.Fatalf("sibling dll missing: %v", err)
	}
	template := filepath.Join(filepath.Dir(dll), "Templates", "go-json", "table.tpl")
	if _, err := os.Stat(template); err != nil {
		t.Fatalf("bin include template missing: %v", err)
	}
}

func TestInstallGoToolUsesGOBIN(t *testing.T) {
	projectDir := t.TempDir()
	manifest := Manifest{Tools: map[string]Tool{
		toolchain.ToolProtocGenGo: {
			Version: "v1.0.0",
			Type:    TypeGoInstall,
			Module:  "example.com/protoc-gen-go",
		},
	}}
	_, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolProtocGenGo},
		GoExe:      "fake-go",
		GoInstall: func(ctx context.Context, goExe string, moduleVersion string, gobin string) error {
			if goExe != "fake-go" || moduleVersion != "example.com/protoc-gen-go@v1.0.0" {
				t.Fatalf("go install args = %s %s", goExe, moduleVersion)
			}
			return os.WriteFile(filepath.Join(gobin, exeName("protoc-gen-go")), []byte("fake"), 0755)
		},
	})
	if len(results) != 1 || results[0].Status != StatusInstalled || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
}

func TestDefaultToolsDoNotIncludeWails(t *testing.T) {
	for _, name := range DefaultTools() {
		if name == toolchain.ToolWails {
			t.Fatal("wails should be an opt-in desktop tool, not a default install tool")
		}
		if name == toolchain.ToolNSIS {
			t.Fatal("nsis should be an opt-in desktop packaging tool, not a default install tool")
		}
	}
}

func TestDefaultManifestIncludesWails(t *testing.T) {
	plan, err := DefaultManifest().Plan(toolchain.ToolWails, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("Plan wails error = %v", err)
	}
	if plan.Type != TypeGoInstall || plan.Module != "github.com/wailsapp/wails/v3/cmd/wails3" || plan.Version != "v3.0.0-beta.28" {
		t.Fatalf("wails plan = %+v", plan)
	}
}

func TestDefaultManifestIncludesNSIS(t *testing.T) {
	plan, err := DefaultManifest().Plan(toolchain.ToolNSIS, "", "windows", "amd64")
	if err != nil {
		t.Fatalf("Plan nsis error = %v", err)
	}
	if plan.Type != TypeArchive || plan.Archive != "zip" || plan.Version != "3.10" || !strings.Contains(plan.URL, "nsis-3.10.zip") {
		t.Fatalf("nsis plan = %+v", plan)
	}
}

func TestDefaultManifestNSISUnsupportedOnNonWindows(t *testing.T) {
	plan, err := DefaultManifest().Plan(toolchain.ToolNSIS, "", "darwin", "amd64")
	if err != nil {
		t.Fatalf("Plan nsis darwin error = %v", err)
	}
	if plan.Type != TypeUnsupported || plan.UnsupportedReason == "" {
		t.Fatalf("nsis darwin plan = %+v", plan)
	}
}

func TestExplicitUnsupportedRedisOnWindows(t *testing.T) {
	_, results := Install(context.Background(), Options{
		ProjectDir: t.TempDir(),
		Manifest:   DefaultManifest(),
		Tools:      []string{toolchain.ToolRedis},
		GOOS:       "windows",
		GOARCH:     "amd64",
	})
	if len(results) != 1 || results[0].Status != StatusUnsupported || !errors.Is(results[0].Err, ErrUnsupported) {
		t.Fatalf("results = %+v", results)
	}
}

func TestDryRunDoesNotWriteFiles(t *testing.T) {
	projectDir := t.TempDir()
	_, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		DryRun:     true,
		Manifest: Manifest{Tools: map[string]Tool{
			toolchain.ToolProtoc: {Version: "1.0.0", Type: TypeArchive, URL: "http://example.invalid/protoc.zip"},
		}},
		Tools: []string{toolchain.ToolProtoc},
	})
	if len(results) != 1 || results[0].Status != StatusDryRun {
		t.Fatalf("results = %+v", results)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".aifd")); !os.IsNotExist(err) {
		t.Fatalf(".aifd should not exist after dry-run: %v", err)
	}
}

func TestArchivePathTraversalRejected(t *testing.T) {
	archive := zipBytes(t, map[string]string{
		"../evil": "bad",
	})
	server := serveBytes(archive)
	defer server.Close()
	projectDir := t.TempDir()
	manifest := Manifest{Tools: map[string]Tool{
		toolchain.ToolProtoc: {
			Version: "1.0.0",
			Type:    TypeArchive,
			URL:     server.URL,
			Archive: "zip",
			Bin:     []string{"protoc"},
		},
	}}
	_, results := Install(context.Background(), Options{
		ProjectDir: projectDir,
		Manifest:   manifest,
		Tools:      []string{toolchain.ToolProtoc},
	})
	if len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Message, "unsafe archive path") {
		t.Fatalf("results = %+v", results)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "evil")); !os.IsNotExist(err) {
		t.Fatalf("unsafe file exists: %v", err)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
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

func tarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		data := []byte(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatalf("WriteHeader(%s) error = %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("Write(%s) error = %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close error = %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close error = %v", err)
	}
	return buf.Bytes()
}

func serveBytes(data []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
