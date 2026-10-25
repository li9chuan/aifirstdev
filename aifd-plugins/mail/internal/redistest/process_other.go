//go:build !windows

package redistest

import "os/exec"

func hideWindow(*exec.Cmd) {}
