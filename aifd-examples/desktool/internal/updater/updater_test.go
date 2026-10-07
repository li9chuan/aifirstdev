package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWithoutManifestURL(t *testing.T) {
	info := Check("", "0.1.0")
	if info.Configured {
		t.Fatal("Configured = true, want false")
	}
	if info.Message == "" {
		t.Fatal("Message is empty")
	}
}

func TestCheckManifestAvailableAndForceByMinVersion(t *testing.T) {
	sum := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version":"0.2.0","minVersion":"0.1.5","url":"https://example.com/desktool.exe","sha256":%q,"notes":["更新"]}`, sum)
	}))
	defer server.Close()
	info := Check(server.URL, "0.1.0")
	if !info.Configured || !info.Available || !info.Force {
		t.Fatalf("info = %+v, want configured available force", info)
	}
	if info.Version != "0.2.0" || info.URL == "" || len(info.Notes) != 1 {
		t.Fatalf("info = %+v, want manifest fields", info)
	}
}

func TestCheckManifestRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "missing version", body: `{"url":"https://example.com/desktool.exe","sha256":"` + strings.Repeat("a", 64) + `"}`},
		{name: "bad url", body: `{"version":"0.2.0","url":"https://example.com/desktool.zip","sha256":"` + strings.Repeat("a", 64) + `"}`},
		{name: "bad sha", body: `{"version":"0.2.0","url":"https://example.com/desktool.exe","sha256":"bad"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			info := Check(server.URL, "0.1.0")
			if !info.Invalid || info.Available {
				t.Fatalf("info = %+v, want invalid unavailable", info)
			}
			if info.Message == "" {
				t.Fatal("invalid manifest message is empty")
			}
		})
	}
}

func TestCheckManifestAllowsMissingURLWhenNoUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":"0.1.0"}`)
	}))
	defer server.Close()
	info := Check(server.URL, "0.1.0")
	if info.Invalid || info.Available {
		t.Fatalf("info = %+v, want valid no update", info)
	}
	if info.Message != "当前已是最新版本" {
		t.Fatalf("message = %q, want latest", info.Message)
	}
}

func TestDownloadAndVerify(t *testing.T) {
	body := []byte("desktool installer")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	result := Download(UpdateInfo{
		Available: true,
		URL:       server.URL + "/desktool.exe",
		SHA256:    hex.EncodeToString(sum[:]),
	})
	if !result.OK {
		t.Fatalf("Download OK = false, message=%q", result.Message)
	}
	defer os.Remove(result.File)
	if result.Bytes != int64(len(body)) {
		t.Fatalf("Bytes = %d, want %d", result.Bytes, len(body))
	}
	if err := Verify(result.File, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("Verify error = %v", err)
	}
}

func TestDownloadRejectsBadSHA256(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bad"))
	}))
	defer server.Close()
	result := Download(UpdateInfo{Available: true, URL: server.URL + "/desktool.exe", SHA256: "00"})
	if result.OK {
		t.Fatalf("Download OK = true, file=%q", result.File)
	}
}

func TestDownloadRejectsNonExeURL(t *testing.T) {
	result := Download(UpdateInfo{Available: true, URL: "https://example.com/desktool.zip"})
	if result.OK {
		t.Fatal("Download OK = true, want false")
	}
	if result.Message == "" {
		t.Fatal("Download message is empty")
	}
}

func TestInstallUnsupportedPlatform(t *testing.T) {
	oldGOOS := currentGOOS
	currentGOOS = "darwin"
	defer func() { currentGOOS = oldGOOS }()
	if err := Install("desktool.exe"); err == nil {
		t.Fatal("Install error = nil, want unsupported")
	}
}

func TestInstallWindowsUsesExecutor(t *testing.T) {
	oldGOOS := currentGOOS
	oldStart := startInstaller
	currentGOOS = "windows"
	defer func() {
		currentGOOS = oldGOOS
		startInstaller = oldStart
	}()
	dir := t.TempDir()
	installer := filepath.Join(dir, "desktool.exe")
	if err := os.WriteFile(installer, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got string
	startInstaller = func(file string) error {
		got = file
		return nil
	}
	if err := Install(installer); err != nil {
		t.Fatalf("Install error = %v", err)
	}
	if got != installer {
		t.Fatalf("installer = %q, want %q", got, installer)
	}
}
