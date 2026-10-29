package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateCacheRemovesBroadExistingFileACL(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "synthetic-private-key")
	if err := os.WriteFile(key, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	world, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, _ := world.DACL()
	if err := windows.SetNamedSecurityInfo(key, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := secureDirectory(dir); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{dir, key} {
		sd, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		sddl := sd.String()
		for _, broad := range []string{";;;WD)", ";;;BU)", ";;;AU)"} {
			if strings.Contains(sddl, broad) {
				t.Fatal("certificate storage grants access to a general user group")
			}
		}
	}
}
