//go:build !windows

package httpserver

import (
	"fmt"
	"os"
)

func secureDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("certificate storage must be a private directory (0700)")
	}
	return nil
}
