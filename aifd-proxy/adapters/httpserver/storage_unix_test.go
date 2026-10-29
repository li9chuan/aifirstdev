//go:build !windows

package httpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateCacheDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certificates")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := secureDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := secureDirectory(dir); err == nil {
		t.Fatal("group-readable certificate directory was accepted")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := secureDirectory(link); err == nil {
		t.Fatal("linked certificate directory was accepted")
	}
}
